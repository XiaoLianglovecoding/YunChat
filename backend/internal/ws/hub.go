package ws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"my-im/internal/apperror"
	authtoken "my-im/internal/auth"
	"my-im/internal/protocol"
)

const (
	defaultPresenceTTL = 60 * time.Second
	defaultPingEvery   = 20 * time.Second
	defaultWriteWait   = 10 * time.Second
	defaultMaxFrame    = 1 << 20
)

var ErrConnectionBackpressure = errors.New("websocket connection backpressure")

type AccessTokenVerifier interface {
	ParseAccess(string) (*authtoken.Claims, error)
}

type PresenceStore interface {
	ClaimPresence(context.Context, int64, string, time.Duration) (bool, error)
	RenewPresence(context.Context, int64, string, time.Duration) (bool, error)
	ReleasePresence(context.Context, int64, string) (bool, error)
}

type FriendLookup interface {
	ListFriendIDsForCache(context.Context, int64) ([]int64, error)
}

type HubOptions struct {
	AllowedOrigins []string
	PresenceTTL    time.Duration
	PingEvery      time.Duration
	WriteWait      time.Duration
	Logger         *zap.Logger
}

// Hub owns this process's active WebSocket connection for each user. Redis's
// compare-and-renew lease is the cross-process online projection; MySQL remains
// the source of durable friend state.
type Hub struct {
	verifier AccessTokenVerifier
	presence PresenceStore
	friends  FriendLookup
	logger   *zap.Logger

	presenceTTL time.Duration
	pingEvery   time.Duration
	writeWait   time.Duration
	upgrader    websocket.Upgrader

	// Claiming the Redis lease and installing the in-process client form one
	// per-user registration operation. Stripes prevent same-user races without
	// making a slow Redis call for Alice block an unrelated login by Bob.
	lifecycleMu     sync.Mutex
	registrationWG  sync.WaitGroup
	registrationMux [64]sync.Mutex
	closed          bool
	mu              sync.RWMutex
	clients         map[int64]*client
	clientsWG       sync.WaitGroup
}

type client struct {
	hub          *Hub
	userID       int64
	connectionID string
	socket       *websocket.Conn
	send         chan []byte
	done         chan struct{}
	closeOnce    sync.Once
	writeMu      sync.Mutex
}

func NewHub(verifier AccessTokenVerifier, presence PresenceStore, friends FriendLookup, opts HubOptions) (*Hub, error) {
	if verifier == nil || presence == nil || friends == nil {
		return nil, errors.New("websocket hub dependencies must not be nil")
	}
	if opts.PresenceTTL <= 0 {
		opts.PresenceTTL = defaultPresenceTTL
	}
	if opts.PingEvery <= 0 {
		opts.PingEvery = defaultPingEvery
	}
	if opts.WriteWait <= 0 {
		opts.WriteWait = defaultWriteWait
	}
	if opts.PingEvery >= opts.PresenceTTL {
		return nil, errors.New("websocket ping interval must be shorter than presence TTL")
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	h := &Hub{
		verifier: verifier, presence: presence, friends: friends, logger: opts.Logger,
		presenceTTL: opts.PresenceTTL, pingEvery: opts.PingEvery, writeWait: opts.WriteWait,
		clients: make(map[int64]*client),
	}
	h.upgrader = websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		CheckOrigin:      originChecker(opts.AllowedOrigins),
	}
	return h, nil
}

// Handler authenticates before upgrading. The browser client uses ?token=;
// Authorization: Bearer is also accepted for non-browser clients.
func (h *Hub) Handler(c *gin.Context) {
	rawToken := strings.TrimSpace(c.Query("token"))
	if rawToken == "" {
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			rawToken = parts[1]
		}
	}
	claims, err := h.verifier.ParseAccess(rawToken)
	if err != nil {
		writeHTTPError(c, apperror.CodeInvalidToken)
		return
	}
	connectionID, err := newConnectionID()
	if err != nil {
		writeHTTPError(c, apperror.CodeInternalFailure)
		return
	}
	socket, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Debug("websocket_upgrade_failed", zap.Error(err))
		return
	}
	current := &client{
		hub: h, userID: claims.UserID, connectionID: connectionID,
		socket: socket, send: make(chan []byte, 64), done: make(chan struct{}),
	}
	if !h.beginRegistration() {
		_ = socket.Close()
		return
	}
	defer h.registrationWG.Done()
	registration := h.userRegistrationLock(current.userID)
	registration.Lock()
	wasOffline, err := h.presence.ClaimPresence(c.Request.Context(), current.userID, current.connectionID, h.presenceTTL)
	if err != nil {
		registration.Unlock()
		_ = socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "presence unavailable"), time.Now().Add(h.writeWait))
		_ = socket.Close()
		h.logger.Warn("websocket_presence_claim_failed", zap.Int64("user_id", current.userID), zap.Error(err))
		return
	}
	old := h.install(current)
	h.clientsWG.Add(1)
	go func() {
		defer h.clientsWG.Done()
		current.run()
	}()
	registration.Unlock()
	// Socket writes can wait for writeWait, so kick outside the per-user
	// registration critical section. A third login can still safely replace B
	// while B is finishing the kick of A.
	if old != nil {
		old.kickAndClose()
	}
	if wasOffline {
		h.broadcastPresence(current.userID, true)
	}
}

// Notify publishes a typed, best-effort online hint. Durable state must have
// been committed before this method is called.
func (h *Hub) Notify(_ context.Context, userID int64, msgType string, data any) error {
	raw, err := protocol.EncodeMsg(msgType, data)
	if err != nil {
		return fmt.Errorf("encode websocket event %s: %w", msgType, err)
	}
	h.mu.RLock()
	current := h.clients[userID]
	h.mu.RUnlock()
	if current == nil {
		return nil
	}
	select {
	case current.send <- raw:
		return nil
	default:
		current.close()
		return ErrConnectionBackpressure
	}
}

func (h *Hub) NotifyFriendApply(ctx context.Context, userID int64, payload protocol.FriendApplyPayload) error {
	return h.Notify(ctx, userID, protocol.TypeFriendApply, payload)
}

func (h *Hub) NotifyFriendAccepted(ctx context.Context, userID int64, payload protocol.FriendAcceptedPayload) error {
	return h.Notify(ctx, userID, protocol.TypeFriendAccepted, payload)
}

// NotifyFriendEvent implements service.FriendEventNotifier without making the
// WebSocket package depend on the service package.
func (h *Hub) NotifyFriendEvent(ctx context.Context, userID int64, msgType string, payload any) error {
	return h.Notify(ctx, userID, msgType, payload)
}

// NotifyGroupEvent implements service.GroupEventNotifier without importing the
// service package. Like every Hub notification, it only reaches a connection
// owned by this process. MySQL and the cache-reconciliation event remain the
// durable truth; a future cross-instance event bus may fan out the same public
// groupAdded/groupUpdated/groupRemoved contract to hubs in other processes.
func (h *Hub) NotifyGroupEvent(ctx context.Context, userID int64, msgType string, payload any) error {
	return h.Notify(ctx, userID, msgType, payload)
}

func (h *Hub) install(next *client) *client {
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.clients[next.userID]
	h.clients[next.userID] = next
	return old
}

func (h *Hub) beginRegistration() bool {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	if h.closed {
		return false
	}
	h.registrationWG.Add(1)
	return true
}

func (h *Hub) userRegistrationLock(userID int64) *sync.Mutex {
	index := uint64(userID) % uint64(len(h.registrationMux))
	return &h.registrationMux[index]
}

func (h *Hub) remove(current *client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[current.userID] != current {
		return false
	}
	delete(h.clients, current.userID)
	return true
}

func (c *client) run() {
	c.socket.SetReadLimit(defaultMaxFrame)
	_ = c.socket.SetReadDeadline(time.Now().Add(c.hub.presenceTTL + c.hub.pingEvery))
	c.socket.SetPongHandler(func(string) error {
		return c.socket.SetReadDeadline(time.Now().Add(c.hub.presenceTTL + c.hub.pingEvery))
	})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		c.writeLoop()
	}()
	defer func() {
		c.hub.disconnect(c)
		<-writerDone
	}()
	for {
		if _, _, err := c.socket.ReadMessage(); err != nil {
			return
		}
		// Chat message dispatch belongs to MSG/WS tasks. Control frames and the
		// connection lifecycle are fully handled here; durable friend changes use HTTP.
	}
}

func (c *client) writeLoop() {
	ticker := time.NewTicker(c.hub.pingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case raw := <-c.send:
			select {
			case <-c.done:
				return
			default:
			}
			c.writeMu.Lock()
			_ = c.socket.SetWriteDeadline(time.Now().Add(c.hub.writeWait))
			err := c.socket.WriteMessage(websocket.TextMessage, raw)
			c.writeMu.Unlock()
			if err != nil {
				c.close()
				return
			}
		case <-ticker.C:
			select {
			case <-c.done:
				return
			default:
			}
			ctx, cancel := context.WithTimeout(context.Background(), c.hub.writeWait)
			owned, err := c.hub.presence.RenewPresence(ctx, c.userID, c.connectionID, c.hub.presenceTTL)
			cancel()
			if err != nil {
				c.hub.logger.Warn("websocket_presence_renew_failed", zap.Int64("user_id", c.userID), zap.Error(err))
				continue
			}
			if !owned {
				c.close()
				return
			}
			c.writeMu.Lock()
			err = c.socket.WriteControl(websocket.PingMessage, nil, time.Now().Add(c.hub.writeWait))
			c.writeMu.Unlock()
			if err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *client) kickAndClose() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		_ = c.socket.SetWriteDeadline(time.Now().Add(c.hub.writeWait))
		_ = c.socket.WriteJSON(map[string]any{"type": protocol.TypeKick, "reason": "new_login"})
		_ = c.socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "new login"), time.Now().Add(c.hub.writeWait))
		_ = c.socket.Close()
	})
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.socket.Close()
	})
}

func (h *Hub) disconnect(current *client) {
	current.close()
	h.remove(current)
	ctx, cancel := context.WithTimeout(context.Background(), h.writeWait)
	released, err := h.presence.ReleasePresence(ctx, current.userID, current.connectionID)
	cancel()
	if err != nil {
		h.logger.Warn("websocket_presence_release_failed", zap.Int64("user_id", current.userID), zap.Error(err))
		return
	}
	if released {
		h.broadcastPresence(current.userID, false)
	}
}

func (h *Hub) broadcastPresence(userID int64, online bool) {
	ctx, cancel := context.WithTimeout(context.Background(), h.writeWait)
	friendIDs, err := h.friends.ListFriendIDsForCache(ctx, userID)
	cancel()
	if err != nil {
		h.logger.Warn("presence_friend_lookup_failed", zap.Int64("user_id", userID), zap.Error(err))
		return
	}
	payload := protocol.PresencePayload{UserID: userID, Online: online}
	for _, friendID := range friendIDs {
		if err := h.Notify(context.Background(), friendID, protocol.TypePresence, payload); err != nil && !errors.Is(err, ErrConnectionBackpressure) {
			h.logger.Warn("presence_notify_failed", zap.Int64("user_id", friendID), zap.Error(err))
		}
	}
}

func (h *Hub) Close() {
	h.lifecycleMu.Lock()
	if h.closed {
		h.lifecycleMu.Unlock()
		return
	}
	h.closed = true
	h.lifecycleMu.Unlock()
	// No registration can Add a client after this wait: beginRegistration and
	// the closed transition are serialized by lifecycleMu.
	h.registrationWG.Wait()

	h.mu.RLock()
	clients := make([]*client, 0, len(h.clients))
	for _, current := range h.clients {
		clients = append(clients, current)
	}
	h.mu.RUnlock()
	for _, current := range clients {
		current.close()
	}
	h.clientsWG.Wait()
}

func writeHTTPError(c *gin.Context, code apperror.Code) {
	err := apperror.New(code)
	c.AbortWithStatusJSON(err.HTTPStatus, gin.H{"code": int(err.Code), "message": err.Message})
}

func newConnectionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func originChecker(allowed []string) func(*http.Request) bool {
	set := make(map[string]struct{}, len(allowed))
	allowAll := false
	for _, origin := range allowed {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		if origin == "*" {
			allowAll = true
		}
		if origin != "" {
			set[origin] = struct{}{}
		}
	}
	return func(request *http.Request) bool {
		origin := strings.TrimRight(strings.TrimSpace(request.Header.Get("Origin")), "/")
		if origin == "" || allowAll {
			return true
		}
		if _, ok := set[origin]; ok {
			return true
		}
		parsed, err := url.Parse(origin)
		return err == nil && strings.EqualFold(parsed.Host, request.Host)
	}
}
