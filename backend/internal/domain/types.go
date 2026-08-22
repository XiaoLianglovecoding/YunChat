package domain

import (
	"encoding/json"
	"time"
)

type UserID uint64
type DeviceID uint64
type ConversationID uint64
type MessageID uint64

type UserStatus uint8

const (
	UserStatusActive    UserStatus = 1
	UserStatusSuspended UserStatus = 2
	UserStatusClosed    UserStatus = 3
)

type ConversationType uint8

const (
	ConversationTypeDirect ConversationType = 1
	ConversationTypeGroup  ConversationType = 2
)

type MemberRole uint8

const (
	MemberRoleMember MemberRole = 1
	MemberRoleAdmin  MemberRole = 2
	MemberRoleOwner  MemberRole = 3
)

type MessageType uint8

const (
	MessageTypeText   MessageType = 1
	MessageTypeImage  MessageType = 2
	MessageTypeFile   MessageType = 3
	MessageTypeAudio  MessageType = 4
	MessageTypeVideo  MessageType = 5
	MessageTypeSystem MessageType = 6
)

type RequestStatus uint8

const (
	RequestStatusPending  RequestStatus = 1
	RequestStatusAccepted RequestStatus = 2
	RequestStatusRejected RequestStatus = 3
	RequestStatusCanceled RequestStatus = 4
)

type User struct {
	ID         UserID     `json:"id"`
	Username   string     `json:"username"`
	Email      string     `json:"email"`
	Phone      string     `json:"phone,omitempty"`
	Nickname   string     `json:"nickname"`
	AvatarURL  string     `json:"avatar_url"`
	Bio        string     `json:"bio"`
	Status     UserStatus `json:"status"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type UserSettings struct {
	UserID                UserID          `json:"user_id"`
	Locale                string          `json:"locale"`
	Theme                 string          `json:"theme"`
	NotificationEnabled   bool            `json:"notification_enabled"`
	MessagePreviewEnabled bool            `json:"message_preview_enabled"`
	Extra                 json.RawMessage `json:"extra,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

type Device struct {
	ID           DeviceID
	UserID       UserID
	DeviceKey    string
	DeviceName   string
	Platform     string
	LastActiveAt time.Time
	RevokedAt    *time.Time
}

type Session struct {
	TokenID   uint64
	UserID    UserID
	DeviceID  DeviceID
	FamilyID  uint64
	TokenHash []byte
	ExpiresAt time.Time
}

type FriendRequest struct {
	ID          uint64
	RequesterID UserID
	AddresseeID UserID
	Message     string
	Status      RequestStatus
	HandledAt   *time.Time
	CreatedAt   time.Time
}

type Contact struct {
	OwnerID   UserID
	ContactID UserID
	Alias     string
	IsStarred bool
	IsMuted   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Conversation struct {
	ID            ConversationID
	Type          ConversationType
	DirectKey     string
	OwnerID       *UserID
	Title         string
	AvatarURL     string
	Status        uint8
	LastSeq       uint64
	LastMessageID *MessageID
	LastMessageAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ConversationMember struct {
	ConversationID ConversationID
	UserID         UserID
	Role           MemberRole
	Alias          string
	JoinSeq        uint64
	LastDelivered  uint64
	LastRead       uint64
	HiddenBefore   uint64
	IsPinned       bool
	IsMuted        bool
	MutedUntil     *time.Time
	JoinedAt       time.Time
	LeftAt         *time.Time
}

type Message struct {
	ID              MessageID
	ConversationID  ConversationID
	ConversationSeq uint64
	ClientMessageID string
	SenderID        UserID
	Type            MessageType
	Body            string
	ReplyToMessage  *MessageID
	Status          uint8
	SentAt          time.Time
	EditedAt        *time.Time
	RevokedAt       *time.Time
}

type OutboxEvent struct {
	ID            uint64
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       json.RawMessage
	Attempts      uint32
	AvailableAt   time.Time
	PublishedAt   *time.Time
	LastError     string
	CreatedAt     time.Time
}

type CursorPage[T any] struct {
	Items      []T
	NextCursor string
}
