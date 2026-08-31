// Package service 保存用例层接口。每个方法的 TODO 编号都能在 DEVELOPMENT_TASKS.md 中查到。
package service

import (
	"context"
	"io"
	"time"

	"github.com/example/my-im/internal/model"
)

type Page[T any] struct {
	Items  []T
	Total  int64
	Limit  int
	Offset int
}

type RegisterCommand struct{ Username, Password string }
type RegisterResult struct {
	UserID   int64
	Username string
}
type LoginCommand struct{ Username, Password string }
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
}

type AuthService interface {
	Register(context.Context, RegisterCommand) (RegisterResult, error) // TODO[AUTH-001]
	Login(context.Context, LoginCommand) (TokenPair, error)            // TODO[AUTH-002]
	Refresh(context.Context, string) (TokenPair, error)                // TODO[AUTH-003]
	UpdateUsername(context.Context, int64, string) (TokenPair, error)  // TODO[AUTH-005]
	UpdatePassword(context.Context, int64, string, string) error       // TODO[AUTH-006]
}

type FriendService interface {
	SendRequest(context.Context, int64, int64, string) (*model.FriendRequest, error)  // TODO[FRIEND-001]
	ListRequests(context.Context, int64, int, int) (Page[model.FriendRequest], error) // TODO[FRIEND-001]
	AcceptRequest(context.Context, int64, int64) (AcceptFriendResult, error)          // TODO[FRIEND-002]
	RejectRequest(context.Context, int64, int64) error                                // TODO[FRIEND-002]
	ListFriends(context.Context, int64, int, int) (Page[model.Friendship], error)     // TODO[FRIEND-003]
	DeleteFriend(context.Context, int64, int64) error                                 // TODO[FRIEND-003]
	Block(context.Context, int64, int64) error                                        // TODO[FRIEND-004]
	Unblock(context.Context, int64, int64) error                                      // TODO[FRIEND-004]
}

type AcceptFriendResult struct {
	UserID   int64 `json:"user_id"`
	FriendID int64 `json:"friend_id"`
}

type GroupMemberListItem struct {
	model.GroupMember
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type GroupService interface {
	Create(context.Context, int64, string, string) (int64, error)                           // TODO[GROUP-001]
	ListByUser(context.Context, int64) ([]model.Group, error)                               // TODO[GROUP-001]
	Get(context.Context, int64, int64) (*model.Group, error)                                // TODO[GROUP-001]
	Update(context.Context, int64, int64, string, string) error                             // TODO[GROUP-001]
	AddMember(context.Context, int64, int64, int64) error                                   // TODO[GROUP-002]
	RemoveMember(context.Context, int64, int64, int64) error                                // TODO[GROUP-002]
	ListMembers(context.Context, int64, int64, int, int) (Page[GroupMemberListItem], error) // TODO[GROUP-002]
	UpdateRole(context.Context, int64, int64, int64, int) error                             // TODO[GROUP-003]
	MuteMember(context.Context, int64, int64, int64, *time.Time) error                      // TODO[GROUP-003]
	TransferOwnership(context.Context, int64, int64, int64) error                           // TODO[GROUP-004]
	Leave(context.Context, int64, int64) error                                              // TODO[GROUP-004]
}

type MessageService interface {
	Send(context.Context, int64, model.SendMessage) (*model.ServerAck, error)              // TODO[MSG-001,MSG-002]
	DeliveryAck(context.Context, int64, model.DeliverAck) error                            // TODO[MSG-003]
	ReadAck(context.Context, int64, model.ReadAck) error                                   // TODO[MSG-003]
	Sync(context.Context, int64, model.SyncReq) (*model.SyncBatch, *model.ConvSync, error) // TODO[MSG-004]
	Revoke(context.Context, int64, model.RevokeMsgReq) error                               // TODO[MSG-005]
	Delete(context.Context, int64, int64, string) error                                    // TODO[MSG-006]
	Search(context.Context, int64, string, int, int) (Page[model.PrivateMessage], error)   // TODO[MSG-006]
}

type MomentService interface {
	Publish(context.Context, int64, string, *string, int) (int64, error)            // TODO[MOMENT-001]
	Get(context.Context, int64, int64) (*model.Moment, error)                       // TODO[MOMENT-001]
	ListByUser(context.Context, int64, int64, int, int) (Page[model.Moment], error) // TODO[MOMENT-001]
	Delete(context.Context, int64, int64) error                                     // TODO[MOMENT-001]
	Feed(context.Context, int64, string, int) ([]model.Moment, string, error)       // TODO[MOMENT-002]
	Like(context.Context, int64, int64) (bool, int64, error)                        // TODO[MOMENT-003]
	Unlike(context.Context, int64, int64) (bool, int64, error)                      // TODO[MOMENT-003]
	Likers(context.Context, int64, int64) ([]model.MomentLiker, error)              // TODO[MOMENT-003]
	Comment(context.Context, int64, int64, string) (int64, error)                   // TODO[MOMENT-004]
	DeleteComment(context.Context, int64, int64) error                              // TODO[MOMENT-004]
}

type SettingsService interface {
	Get(context.Context, int64) (*model.UserSettings, error) // TODO[SETTINGS-001]
	Update(context.Context, int64, model.UserSettings) error // TODO[SETTINGS-001]
	Mute(context.Context, int64, string) error               // TODO[SETTINGS-002]
	Unmute(context.Context, int64, string) error             // TODO[SETTINGS-002]
}

type UploadResult struct {
	URL      string
	FilePath string
	Size     int64
}

type UploadService interface {
	UploadAvatar(context.Context, int64, string, io.Reader) (UploadResult, error) // TODO[UPLOAD-001]
}
