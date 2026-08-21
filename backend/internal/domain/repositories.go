package domain

import (
	"context"
	"time"
)

type UserRepository interface {
	Create(ctx context.Context, user User, passwordHash string) error
	FindByID(ctx context.Context, id UserID) (User, error)
	FindByLogin(ctx context.Context, login string) (User, string, error)
	UpdateProfile(ctx context.Context, user User) error
}

type ContactRepository interface {
	CreateRequest(ctx context.Context, request FriendRequest) error
	ListRequests(ctx context.Context, userID UserID, cursor string, limit int) (CursorPage[FriendRequest], error)
	AcceptRequest(ctx context.Context, requestID uint64, actorID UserID, handledAt time.Time) error
	ListContacts(ctx context.Context, userID UserID, cursor string, limit int) (CursorPage[Contact], error)
}

type ConversationRepository interface {
	CreateDirect(ctx context.Context, conversation Conversation, members []ConversationMember) error
	CreateGroup(ctx context.Context, conversation Conversation, members []ConversationMember) error
	FindByID(ctx context.Context, id ConversationID) (Conversation, error)
	ListByUser(ctx context.Context, userID UserID, cursor string, limit int) (CursorPage[Conversation], error)
	IsActiveMember(ctx context.Context, conversationID ConversationID, userID UserID) (bool, error)
	UpdateReadSeq(ctx context.Context, conversationID ConversationID, userID UserID, seq uint64) error
}

type MessageRepository interface {
	Append(ctx context.Context, message Message, event OutboxEvent) (Message, error)
	FindByClientID(ctx context.Context, senderID UserID, clientMessageID string) (Message, error)
	ListBefore(ctx context.Context, conversationID ConversationID, beforeSeq uint64, limit int) ([]Message, error)
	Recall(ctx context.Context, messageID MessageID, actorID UserID, now time.Time) error
}

type OutboxRepository interface {
	Claim(ctx context.Context, limit int, now time.Time) ([]OutboxEvent, error)
	MarkPublished(ctx context.Context, eventID uint64, publishedAt time.Time) error
	MarkFailed(ctx context.Context, eventID uint64, reason string, retryAt time.Time) error
}

type TransactionManager interface {
	WithinTransaction(ctx context.Context, fn func(context.Context) error) error
}
