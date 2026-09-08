package ws

import (
	"context"
	"strconv"
	"testing"
	"time"

	authtoken "my-im/internal/auth"
	"my-im/internal/model"
	"my-im/internal/protocol"
)

// BenchmarkHubDisbandFanout500 measures the current GROUP-005 upper bound.
// The online case uses in-memory client queues so the result isolates event
// encoding, hub lookup and non-blocking fanout rather than network speed.
//
// Run a stable sample with:
//
//	go test ./internal/ws -run '^$' -bench BenchmarkHubDisbandFanout500 -benchmem -count=5
func BenchmarkHubDisbandFanout500(b *testing.B) {
	const memberCount = 500
	payload := model.GroupRemovedNotification{GroupID: 42, Reason: model.GroupRemovedReasonDissolved}
	ctx := context.Background()

	b.Run("offline", func(b *testing.B) {
		hub := benchmarkGroupHub(b)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			for userID := int64(1); userID <= memberCount; userID++ {
				if err := hub.NotifyGroupEvent(ctx, userID, protocol.TypeGroupRemoved, payload); err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("online_queue", func(b *testing.B) {
		hub := benchmarkGroupHub(b)
		for userID := int64(1); userID <= memberCount; userID++ {
			hub.clients[userID] = &client{
				hub: hub, userID: userID, connectionID: "benchmark-" + strconv.FormatInt(userID, 10),
				send: make(chan []byte, 1), done: make(chan struct{}),
			}
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			for userID := int64(1); userID <= memberCount; userID++ {
				if err := hub.NotifyGroupEvent(ctx, userID, protocol.TypeGroupRemoved, payload); err != nil {
					b.Fatal(err)
				}
				<-hub.clients[userID].send
			}
		}
	})
}

func benchmarkGroupHub(b *testing.B) *Hub {
	b.Helper()
	manager, err := authtoken.NewManager(
		"0123456789abcdef0123456789abcdef", "group-fanout-benchmark", time.Hour, 24*time.Hour,
	)
	if err != nil {
		b.Fatal(err)
	}
	hub, err := NewHub(manager, &memoryPresence{owners: make(map[int64]string)}, memoryFriends{}, HubOptions{})
	if err != nil {
		b.Fatal(err)
	}
	return hub
}
