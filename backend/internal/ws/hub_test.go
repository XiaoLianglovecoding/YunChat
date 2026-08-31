package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	authtoken "my-im/internal/auth"
	"my-im/internal/protocol"
)

type memoryPresence struct {
	mu     sync.Mutex
	owners map[int64]string
}

type gatedPresence struct {
	memoryPresence
	firstClaim   chan struct{}
	releaseFirst chan struct{}
	claimCalls   int
}

func newGatedPresence() *gatedPresence {
	return &gatedPresence{
		memoryPresence: memoryPresence{owners: make(map[int64]string)},
		firstClaim:     make(chan struct{}),
		releaseFirst:   make(chan struct{}),
	}
}

func (p *gatedPresence) ClaimPresence(_ context.Context, userID int64, connectionID string, _ time.Duration) (bool, error) {
	p.mu.Lock()
	_, existed := p.owners[userID]
	p.owners[userID] = connectionID
	p.claimCalls++
	call := p.claimCalls
	p.mu.Unlock()
	if call == 1 {
		close(p.firstClaim)
		<-p.releaseFirst
	}
	return !existed, nil
}

func (p *gatedPresence) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.claimCalls
}

func (p *memoryPresence) ClaimPresence(_ context.Context, userID int64, connectionID string, _ time.Duration) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, existed := p.owners[userID]
	p.owners[userID] = connectionID
	return !existed, nil
}
func (p *memoryPresence) RenewPresence(_ context.Context, userID int64, connectionID string, _ time.Duration) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.owners[userID] == connectionID, nil
}
func (p *memoryPresence) ReleasePresence(_ context.Context, userID int64, connectionID string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owners[userID] != connectionID {
		return false, nil
	}
	delete(p.owners, userID)
	return true, nil
}
func (p *memoryPresence) owner(userID int64) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.owners[userID]
}

type memoryFriends map[int64][]int64

func (f memoryFriends) ListFriendIDsForCache(_ context.Context, userID int64) ([]int64, error) {
	return append([]int64(nil), f[userID]...), nil
}

func TestHubAuthenticatesAndPushesTypedEvent(t *testing.T) {
	hub, manager, server := newHubTestServer(t, memoryFriends{})
	defer hub.Close()
	defer server.Close()

	response, err := http.Get(server.URL + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", response.StatusCode)
	}

	connection := dialUser(t, server.URL, issueAccess(t, manager, 2, "bob"))
	defer connection.Close()
	payload := protocol.FriendApplyPayload{RequestID: 12, FromUserID: 1, Username: "alice", Message: "hi", CreatedAt: time.Now().UTC()}
	if err := hub.NotifyFriendApply(context.Background(), 2, payload); err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Type string                      `json:"type"`
		Data protocol.FriendApplyPayload `json:"data"`
	}
	readJSON(t, connection, &frame)
	if frame.Type != protocol.TypeFriendApply || frame.Data.RequestID != 12 || frame.Data.FromUserID != 1 {
		t.Fatalf("unexpected frame: %+v", frame)
	}
}

func TestHubOldConnectionCannotDeleteNewPresenceLease(t *testing.T) {
	presence := &memoryPresence{owners: make(map[int64]string)}
	manager := testTokenManager(t)
	hub, err := NewHub(manager, presence, memoryFriends{}, HubOptions{PresenceTTL: time.Second, PingEvery: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", hub.Handler)
	server := httptest.NewServer(router)
	defer hub.Close()
	defer server.Close()

	token := issueAccess(t, manager, 7, "alice")
	oldConnection := dialUser(t, server.URL, token)
	defer oldConnection.Close()
	oldOwner := waitForOwner(t, presence, 7, "")
	newConnection := dialUser(t, server.URL, token)
	defer newConnection.Close()
	newOwner := waitForDifferentOwner(t, presence, 7, oldOwner)

	var kick map[string]any
	readJSON(t, oldConnection, &kick)
	if kick["type"] != protocol.TypeKick || kick["reason"] != "new_login" {
		t.Fatalf("unexpected kick: %+v", kick)
	}
	time.Sleep(30 * time.Millisecond)
	if got := presence.owner(7); got != newOwner {
		t.Fatalf("old disconnect removed/replaced new lease: got=%q want=%q", got, newOwner)
	}
}

func TestHubSerializesConcurrentConnectionRegistration(t *testing.T) {
	presence := newGatedPresence()
	manager := testTokenManager(t)
	hub, err := NewHub(manager, presence, memoryFriends{}, HubOptions{
		PresenceTTL: 10 * time.Second,
		PingEvery:   5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", hub.Handler)
	server := httptest.NewServer(router)
	defer hub.Close()
	defer server.Close()

	token := issueAccess(t, manager, 8, "parallel")
	first := dialUser(t, server.URL, token)
	defer first.Close()
	select {
	case <-presence.firstClaim:
	case <-time.After(time.Second):
		t.Fatal("first presence claim did not start")
	}
	second := dialUser(t, server.URL, token)
	defer second.Close()

	// The first registration is intentionally paused after claiming Redis.
	// A second registration must not enter ClaimPresence until the first one
	// has also installed its local client.
	time.Sleep(30 * time.Millisecond)
	if got := presence.calls(); got != 1 {
		close(presence.releaseFirst)
		t.Fatalf("presence claims overlapped: got %d calls before release", got)
	}
	close(presence.releaseFirst)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		presenceOwner := presence.owner(8)
		hub.mu.RLock()
		current := hub.clients[8]
		hub.mu.RUnlock()
		if presence.calls() == 2 && current != nil && current.connectionID == presenceOwner {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("local client and Redis presence owner did not converge")
}

func TestHubDoesNotBlockAnotherUserBehindSlowRegistration(t *testing.T) {
	presence := newGatedPresence()
	manager := testTokenManager(t)
	hub, err := NewHub(manager, presence, memoryFriends{}, HubOptions{
		PresenceTTL: 10 * time.Second,
		PingEvery:   5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", hub.Handler)
	server := httptest.NewServer(router)
	defer hub.Close()
	defer server.Close()

	first := dialUser(t, server.URL, issueAccess(t, manager, 10, "slow"))
	defer first.Close()
	select {
	case <-presence.firstClaim:
	case <-time.After(time.Second):
		t.Fatal("first presence claim did not start")
	}
	second := dialUser(t, server.URL, issueAccess(t, manager, 11, "independent"))
	defer second.Close()

	if !waitUntil(time.Second, func() bool { return presence.calls() == 2 && presence.owner(11) != "" }) {
		close(presence.releaseFirst)
		t.Fatal("unrelated user was blocked behind the slow registration")
	}
	close(presence.releaseFirst)
}

func TestHubBroadcastsPresenceToOnlineFriends(t *testing.T) {
	hub, manager, server := newHubTestServer(t, memoryFriends{1: {2}, 2: {1}})
	defer hub.Close()
	defer server.Close()
	bob := dialUser(t, server.URL, issueAccess(t, manager, 2, "bob"))
	defer bob.Close()
	alice := dialUser(t, server.URL, issueAccess(t, manager, 1, "alice"))
	defer alice.Close()

	var frame struct {
		Type string                   `json:"type"`
		Data protocol.PresencePayload `json:"data"`
	}
	readJSON(t, bob, &frame)
	if frame.Type != protocol.TypePresence || frame.Data.UserID != 1 || !frame.Data.Online {
		t.Fatalf("unexpected presence frame: %+v", frame)
	}
}

func TestHubCloseStopsLongIntervalWritersPromptly(t *testing.T) {
	presence := &memoryPresence{owners: make(map[int64]string)}
	manager := testTokenManager(t)
	hub, err := NewHub(manager, presence, memoryFriends{}, HubOptions{
		PresenceTTL: 10 * time.Second,
		PingEvery:   5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", hub.Handler)
	server := httptest.NewServer(router)
	defer server.Close()

	connection := dialUser(t, server.URL, issueAccess(t, manager, 9, "shutdown"))
	defer connection.Close()
	_ = waitForOwner(t, presence, 9, "")

	closed := make(chan struct{})
	go func() {
		hub.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("hub close waited for the five-second heartbeat ticker")
	}
}

func newHubTestServer(t *testing.T, friends memoryFriends) (*Hub, *authtoken.Manager, *httptest.Server) {
	t.Helper()
	manager := testTokenManager(t)
	presence := &memoryPresence{owners: make(map[int64]string)}
	hub, err := NewHub(manager, presence, friends, HubOptions{PresenceTTL: time.Second, PingEvery: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", hub.Handler)
	return hub, manager, httptest.NewServer(router)
}

func testTokenManager(t *testing.T) *authtoken.Manager {
	t.Helper()
	manager, err := authtoken.NewManager("0123456789abcdef0123456789abcdef", "ws-hub-test", time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func issueAccess(t *testing.T, manager *authtoken.Manager, userID int64, username string) string {
	t.Helper()
	pair, err := manager.IssuePair(userID, username, "")
	if err != nil {
		t.Fatal(err)
	}
	return pair.AccessToken
}

func dialUser(t *testing.T, serverURL, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws?token=" + url.QueryEscape(token)
	connection, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial websocket: %v (status %d)", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	return connection
}

func readJSON(t *testing.T, connection *websocket.Conn, target any) {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	_, raw, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

func waitForOwner(t *testing.T, presence *memoryPresence, userID int64, unwanted string) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if owner := presence.owner(userID); owner != "" && owner != unwanted {
			return owner
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("presence owner was not installed")
	return ""
}

func waitForDifferentOwner(t *testing.T, presence *memoryPresence, userID int64, old string) string {
	return waitForOwner(t, presence, userID, old)
}

func waitUntil(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return condition()
}
