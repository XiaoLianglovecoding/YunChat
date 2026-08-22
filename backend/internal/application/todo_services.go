package application

import (
	"context"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
)

// NewTODO keeps the non-M1 features wired and explicit while M1 uses the real
// account service created by NewServices.
func NewTODO() Services {
	placeholder := todoService{}
	return Services{
		Auth:          todoAuth{},
		Users:         todoUser{},
		Contacts:      placeholder,
		Conversations: placeholder,
		Messages:      placeholder,
	}
}

type todoAuth struct{}

func (todoAuth) Register(context.Context, RegisterCommand) (AuthSession, error) {
	return AuthSession{}, domain.ErrNotImplemented
}
func (todoAuth) Login(context.Context, LoginCommand) (AuthSession, error) {
	return AuthSession{}, domain.ErrNotImplemented
}
func (todoAuth) Refresh(context.Context, string) (AuthSession, error) {
	return AuthSession{}, domain.ErrNotImplemented
}
func (todoAuth) Logout(context.Context, domain.UserID, domain.DeviceID) error {
	return domain.ErrNotImplemented
}
func (todoAuth) ChangePassword(context.Context, domain.UserID, string, string) error {
	return domain.ErrNotImplemented
}

type todoUser struct{}

func (todoUser) GetMe(context.Context, domain.UserID) (domain.User, error) {
	return domain.User{}, domain.ErrNotImplemented
}
func (todoUser) UpdateProfile(context.Context, UpdateProfileCommand) (domain.User, error) {
	return domain.User{}, domain.ErrNotImplemented
}
func (todoUser) GetSettings(context.Context, domain.UserID) (domain.UserSettings, error) {
	return domain.UserSettings{}, domain.ErrNotImplemented
}
func (todoUser) UpdateSettings(context.Context, UpdateSettingsCommand) (domain.UserSettings, error) {
	return domain.UserSettings{}, domain.ErrNotImplemented
}

type todoService struct{}

func (todoService) SendRequest(context.Context, SendFriendRequestCommand) (domain.FriendRequest, error) {
	return domain.FriendRequest{}, domain.ErrNotImplemented
}
func (todoService) ListContacts(context.Context, domain.UserID, string, int) (domain.CursorPage[domain.Contact], error) {
	return domain.CursorPage[domain.Contact]{}, domain.ErrNotImplemented
}
func (todoService) AcceptRequest(context.Context, domain.UserID, uint64) error {
	return domain.ErrNotImplemented
}
func (todoService) RejectRequest(context.Context, domain.UserID, uint64) error {
	return domain.ErrNotImplemented
}
func (todoService) CreateDirect(context.Context, CreateDirectCommand) (domain.Conversation, error) {
	return domain.Conversation{}, domain.ErrNotImplemented
}
func (todoService) CreateGroup(context.Context, CreateGroupCommand) (domain.Conversation, error) {
	return domain.Conversation{}, domain.ErrNotImplemented
}
func (todoService) ListConversations(context.Context, domain.UserID, string, int) (domain.CursorPage[domain.Conversation], error) {
	return domain.CursorPage[domain.Conversation]{}, domain.ErrNotImplemented
}
func (todoService) MarkRead(context.Context, domain.UserID, domain.ConversationID, uint64) error {
	return domain.ErrNotImplemented
}
func (todoService) SendMessage(context.Context, SendMessageCommand) (domain.Message, error) {
	return domain.Message{}, domain.ErrNotImplemented
}
func (todoService) ListMessages(context.Context, domain.UserID, domain.ConversationID, uint64, int) ([]domain.Message, error) {
	return nil, domain.ErrNotImplemented
}
func (todoService) RecallMessage(context.Context, domain.UserID, domain.MessageID) error {
	return domain.ErrNotImplemented
}
