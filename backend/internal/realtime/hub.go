package realtime

import (
	"context"
	"errors"
	"sync"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
)

var ErrDuplicateSession = errors.New("realtime session is already registered")

type Client interface {
	SessionID() string
	UserID() domain.UserID
	Send(context.Context, Envelope) error
	Close(reason string) error
}

type Hub struct {
	mu      sync.RWMutex
	clients map[domain.UserID]map[string]Client
}

func NewHub() *Hub {
	return &Hub{clients: make(map[domain.UserID]map[string]Client)}
}

func (hub *Hub) Register(client Client) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	userClients := hub.clients[client.UserID()]
	if userClients == nil {
		userClients = make(map[string]Client)
		hub.clients[client.UserID()] = userClients
	}
	if _, exists := userClients[client.SessionID()]; exists {
		return ErrDuplicateSession
	}
	userClients[client.SessionID()] = client
	return nil
}

func (hub *Hub) Unregister(client Client) {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	userClients := hub.clients[client.UserID()]
	if userClients == nil {
		return
	}
	if current, exists := userClients[client.SessionID()]; exists && current == client {
		delete(userClients, client.SessionID())
	}
	if len(userClients) == 0 {
		delete(hub.clients, client.UserID())
	}
}

func (hub *Hub) SendToUser(ctx context.Context, userID domain.UserID, event Envelope) []error {
	hub.mu.RLock()
	clients := make([]Client, 0, len(hub.clients[userID]))
	for _, client := range hub.clients[userID] {
		clients = append(clients, client)
	}
	hub.mu.RUnlock()

	var sendErrors []error
	for _, client := range clients {
		if err := client.Send(ctx, event); err != nil {
			sendErrors = append(sendErrors, err)
		}
	}
	return sendErrors
}

func (hub *Hub) Online(userID domain.UserID) bool {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return len(hub.clients[userID]) > 0
}

func (hub *Hub) ConnectionCount() int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	count := 0
	for _, clients := range hub.clients {
		count += len(clients)
	}
	return count
}
