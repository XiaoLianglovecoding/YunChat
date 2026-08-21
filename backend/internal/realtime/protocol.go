package realtime

import (
	"encoding/json"
	"errors"
	"time"
)

const ProtocolVersion = "1.0"

type EventType string

const (
	EventSystemHello      EventType = "system.hello"
	EventSystemPing       EventType = "system.ping"
	EventSystemPong       EventType = "system.pong"
	EventMessageSend      EventType = "message.send"
	EventMessageAck       EventType = "message.ack"
	EventMessageCreated   EventType = "message.created"
	EventMessageRecalled  EventType = "message.recalled"
	EventConversationRead EventType = "conversation.read"
	EventTypingChanged    EventType = "typing.changed"
	EventError            EventType = "error"
)

type Envelope struct {
	ID        string          `json:"id"`
	Type      EventType       `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func NewEnvelope(id string, eventType EventType, data any) (Envelope, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{ID: id, Type: eventType, Timestamp: time.Now().UTC().UnixMilli(), Data: payload}, nil
}

func (event Envelope) Validate() error {
	if event.ID == "" {
		return errors.New("event id is required")
	}
	if event.Type == "" {
		return errors.New("event type is required")
	}
	if len(event.Data) > 1<<20 {
		return errors.New("event data exceeds 1 MiB")
	}
	return nil
}
