package realtime

import (
	"context"
	"testing"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
)

type fakeClient struct {
	userID    domain.UserID
	sessionID string
	received  int
}

func (client *fakeClient) SessionID() string                    { return client.sessionID }
func (client *fakeClient) UserID() domain.UserID                { return client.userID }
func (client *fakeClient) Send(context.Context, Envelope) error { client.received++; return nil }
func (client *fakeClient) Close(string) error                   { return nil }

func TestHubRegistersSendsAndUnregisters(t *testing.T) {
	hub := NewHub()
	client := &fakeClient{userID: 42, sessionID: "session-1"}
	if err := hub.Register(client); err != nil {
		t.Fatal(err)
	}
	if !hub.Online(42) || hub.ConnectionCount() != 1 {
		t.Fatal("client was not registered")
	}
	event, err := NewEnvelope("event-1", EventSystemPing, map[string]string{"value": "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if errs := hub.SendToUser(context.Background(), 42, event); len(errs) != 0 {
		t.Fatalf("unexpected send errors: %v", errs)
	}
	if client.received != 1 {
		t.Fatalf("received = %d, want 1", client.received)
	}
	hub.Unregister(client)
	if hub.Online(42) {
		t.Fatal("client was not unregistered")
	}
}
