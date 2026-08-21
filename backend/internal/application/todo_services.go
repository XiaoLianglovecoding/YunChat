package application

import (
	"context"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
)

// NewTODO returns explicit placeholders so transport wiring can compile before
// any business rule is implemented.
func NewTODO() Services {
	placeholder := todoService{}
	return Services{
		Auth:          placeholder,
		Contacts:      placeholder,
		Conversations: placeholder,
		Messages:      placeholder,
	}
}

type todoService struct{}

func (todoService) Register(context.Context, RegisterCommand) (domain.User, error) {
	// TODO(linknest): validate uniqueness and create user plus credentials in one transaction.
	return domain.User{}, domain.ErrNotImplemented
}

func (todoService) Login(context.Context, LoginCommand) (TokenPair, error) {
	// TODO(linknest): verify credentials, register device, and rotate refresh token.
	return TokenPair{}, domain.ErrNotImplemented
}

func (todoService) Refresh(context.Context, string) (TokenPair, error) {
	// TODO(linknest): implement refresh token family rotation and replay detection.
	return TokenPair{}, domain.ErrNotImplemented
}

func (todoService) Logout(context.Context, domain.UserID, domain.DeviceID) error {
	// TODO(linknest): revoke the device session and disconnect its realtime client.
	return domain.ErrNotImplemented
}

func (todoService) SendRequest(context.Context, SendFriendRequestCommand) (domain.FriendRequest, error) {
	// TODO(linknest): enforce block and duplicate-request rules.
	return domain.FriendRequest{}, domain.ErrNotImplemented
}

func (todoService) ListContacts(context.Context, domain.UserID, string, int) (domain.CursorPage[domain.Contact], error) {
	// TODO(linknest): query contacts with a stable cursor.
	return domain.CursorPage[domain.Contact]{}, domain.ErrNotImplemented
}

func (todoService) AcceptRequest(context.Context, domain.UserID, uint64) error {
	// TODO(linknest): accept request and create both contact rows atomically.
	return domain.ErrNotImplemented
}

func (todoService) RejectRequest(context.Context, domain.UserID, uint64) error {
	// TODO(linknest): transition a pending request to rejected.
	return domain.ErrNotImplemented
}

func (todoService) CreateDirect(context.Context, CreateDirectCommand) (domain.Conversation, error) {
	// TODO(linknest): create or return the unique direct conversation.
	return domain.Conversation{}, domain.ErrNotImplemented
}

func (todoService) CreateGroup(context.Context, CreateGroupCommand) (domain.Conversation, error) {
	// TODO(linknest): create conversation, group profile, and members atomically.
	return domain.Conversation{}, domain.ErrNotImplemented
}

func (todoService) ListConversations(context.Context, domain.UserID, string, int) (domain.CursorPage[domain.Conversation], error) {
	// TODO(linknest): list visible conversations ordered by last activity.
	return domain.CursorPage[domain.Conversation]{}, domain.ErrNotImplemented
}

func (todoService) MarkRead(context.Context, domain.UserID, domain.ConversationID, uint64) error {
	// TODO(linknest): advance the read cursor monotonically and emit an event.
	return domain.ErrNotImplemented
}

func (todoService) SendMessage(context.Context, SendMessageCommand) (domain.Message, error) {
	// TODO(linknest): authorize member, allocate seq, and append message plus outbox event.
	return domain.Message{}, domain.ErrNotImplemented
}

func (todoService) ListMessages(context.Context, domain.UserID, domain.ConversationID, uint64, int) ([]domain.Message, error) {
	// TODO(linknest): authorize member and return cursor-based history.
	return nil, domain.ErrNotImplemented
}

func (todoService) RecallMessage(context.Context, domain.UserID, domain.MessageID) error {
	// TODO(linknest): enforce sender/admin and recall-window rules.
	return domain.ErrNotImplemented
}
