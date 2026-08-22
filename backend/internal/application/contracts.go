package application

import (
	"context"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
)

type RegisterCommand struct {
	Username   string
	Email      string
	Password   string
	Nickname   string
	DeviceKey  string
	DeviceName string
	Platform   string
}

type LoginCommand struct {
	Login      string
	Password   string
	DeviceKey  string
	DeviceName string
	Platform   string
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type AuthSession struct {
	User domain.User `json:"user"`
	TokenPair
}

type SendFriendRequestCommand struct {
	RequesterID domain.UserID
	AddresseeID domain.UserID
	Message     string
}

type CreateDirectCommand struct {
	CreatorID domain.UserID
	PeerID    domain.UserID
}

type CreateGroupCommand struct {
	OwnerID   domain.UserID
	Title     string
	MemberIDs []domain.UserID
}

type SendMessageCommand struct {
	SenderID        domain.UserID
	ConversationID  domain.ConversationID
	ClientMessageID string
	Type            domain.MessageType
	Body            string
	ReplyToMessage  *domain.MessageID
}

type AuthUseCase interface {
	Register(ctx context.Context, cmd RegisterCommand) (AuthSession, error)
	Login(ctx context.Context, cmd LoginCommand) (AuthSession, error)
	Refresh(ctx context.Context, refreshToken string) (AuthSession, error)
	Logout(ctx context.Context, userID domain.UserID, deviceID domain.DeviceID) error
	ChangePassword(ctx context.Context, userID domain.UserID, currentPassword, newPassword string) error
}

type UpdateProfileCommand struct {
	UserID    domain.UserID
	Nickname  *string
	Email     *string
	AvatarURL *string
	Bio       *string
}

type UpdateSettingsCommand struct {
	UserID                domain.UserID
	Locale                *string
	Theme                 *string
	NotificationEnabled   *bool
	MessagePreviewEnabled *bool
	Extra                 []byte
}

type UserUseCase interface {
	GetMe(ctx context.Context, userID domain.UserID) (domain.User, error)
	UpdateProfile(ctx context.Context, cmd UpdateProfileCommand) (domain.User, error)
	GetSettings(ctx context.Context, userID domain.UserID) (domain.UserSettings, error)
	UpdateSettings(ctx context.Context, cmd UpdateSettingsCommand) (domain.UserSettings, error)
}

type ContactUseCase interface {
	SendRequest(ctx context.Context, cmd SendFriendRequestCommand) (domain.FriendRequest, error)
	ListContacts(ctx context.Context, userID domain.UserID, cursor string, limit int) (domain.CursorPage[domain.Contact], error)
	AcceptRequest(ctx context.Context, actorID domain.UserID, requestID uint64) error
	RejectRequest(ctx context.Context, actorID domain.UserID, requestID uint64) error
}

type ConversationUseCase interface {
	CreateDirect(ctx context.Context, cmd CreateDirectCommand) (domain.Conversation, error)
	CreateGroup(ctx context.Context, cmd CreateGroupCommand) (domain.Conversation, error)
	ListConversations(ctx context.Context, userID domain.UserID, cursor string, limit int) (domain.CursorPage[domain.Conversation], error)
	MarkRead(ctx context.Context, userID domain.UserID, conversationID domain.ConversationID, seq uint64) error
}

type MessageUseCase interface {
	SendMessage(ctx context.Context, cmd SendMessageCommand) (domain.Message, error)
	ListMessages(ctx context.Context, userID domain.UserID, conversationID domain.ConversationID, beforeSeq uint64, limit int) ([]domain.Message, error)
	RecallMessage(ctx context.Context, userID domain.UserID, messageID domain.MessageID) error
}

type Services struct {
	Auth          AuthUseCase
	Users         UserUseCase
	Contacts      ContactUseCase
	Conversations ConversationUseCase
	Messages      MessageUseCase
}
