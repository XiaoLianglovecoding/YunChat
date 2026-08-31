package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFriendEventJSONContract(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 31, 12, 30, 0, 0, time.UTC)
	tests := []struct {
		name        string
		messageType string
		payload     any
		wantData    map[string]any
	}{
		{
			name:        "friend application",
			messageType: TypeFriendApply,
			payload: FriendApplyPayload{
				RequestID:  31,
				FromUserID: 7,
				Username:   "alice",
				AvatarURL:  "/uploads/alice.png",
				Message:    "hello",
				CreatedAt:  createdAt,
			},
			wantData: map[string]any{
				"requestId":  float64(31),
				"fromUserId": float64(7),
				"username":   "alice",
				"avatarUrl":  "/uploads/alice.png",
				"message":    "hello",
				"createdAt":  "2026-08-31T12:30:00Z",
			},
		},
		{
			name:        "friend accepted",
			messageType: TypeFriendAccepted,
			payload: FriendAcceptedPayload{
				RequestID: 41,
				UserID:    9,
				FriendID:  7,
				Username:  "bob",
			},
			wantData: map[string]any{
				"requestId": float64(41),
				"userId":    float64(9),
				"friendId":  float64(7),
				"username":  "bob",
			},
		},
		{
			name:        "presence",
			messageType: TypePresence,
			payload: PresencePayload{
				UserID: 7,
				Online: true,
			},
			wantData: map[string]any{
				"userId": float64(7),
				"online": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := EncodeMsg(tt.messageType, tt.payload)
			if err != nil {
				t.Fatalf("EncodeMsg() error = %v", err)
			}

			var envelope struct {
				Type string         `json:"type"`
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if envelope.Type != tt.messageType {
				t.Fatalf("type = %q, want %q", envelope.Type, tt.messageType)
			}
			if len(envelope.Data) != len(tt.wantData) {
				t.Fatalf("data fields = %#v, want %#v", envelope.Data, tt.wantData)
			}
			for key, want := range tt.wantData {
				if got := envelope.Data[key]; got != want {
					t.Errorf("data[%q] = %#v, want %#v", key, got, want)
				}
			}
		})
	}
}
