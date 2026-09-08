// Package service 保存用例层接口。每个方法的 TODO 编号都能在 DEVELOPMENT_TASKS.md 中查到。
package service

import (
	"context"
	"io"
	"time"

	"my-im/internal/model"
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
	AvatarURL    string
}

type AuthService interface {
	Register(context.Context, RegisterCommand) (RegisterResult, error)
	Login(context.Context, LoginCommand) (TokenPair, error)
	Refresh(context.Context, string) (TokenPair, error)
	UpdateUsername(context.Context, int64, string) (TokenPair, error)
	UpdatePassword(context.Context, int64, string, string) error
}

type AvatarProfile struct {
	Username  string
	AvatarURL string
}

type ProfileService interface {
	GetAvatar(context.Context, int64) (AvatarProfile, error)
}

type FriendService interface {
	SendRequest(context.Context, int64, int64, string) (*model.FriendRequest, error)
	ListRequests(context.Context, int64, int, int) (Page[model.FriendRequest], error)
	AcceptRequest(context.Context, int64, int64) (AcceptFriendResult, error)
	RejectRequest(context.Context, int64, int64) error
	ListFriends(context.Context, int64, int, int) (Page[model.Friendship], error)
	DeleteFriend(context.Context, int64, int64) error
	Block(context.Context, int64, int64) error
	Unblock(context.Context, int64, int64) error
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

// GroupProfileService 是 GROUP-001 已落地的资料用例边界。
type GroupProfileService interface {
	Create(context.Context, int64, string, string) (int64, error)
	ListByUser(context.Context, int64) ([]model.Group, error)
	Get(context.Context, int64, int64) (*model.Group, error)
	Update(context.Context, int64, int64, string, string) error
}

type GroupMemberService interface {
	AddMember(context.Context, int64, int64, int64) error
	RemoveMember(context.Context, int64, int64, int64) error
	ListMembers(context.Context, int64, int64, int, int) (Page[GroupMemberListItem], error)
}

type GroupMemberManagementService interface {
	UpdateRole(context.Context, int64, int64, int64, int) error
	MuteMember(context.Context, int64, int64, int64, *time.Time) error
}

type GroupLifecycleService interface {
	TransferOwnership(context.Context, int64, int64, int64) error
	Leave(context.Context, int64, int64) error
}

// GroupDisbandService 是 GROUP-005 的群解散用例边界。
type GroupDisbandService interface {
	Disband(context.Context, int64, int64) error
}

// GroupCoreService 汇总目前已经落地的 GROUP-001～GROUP-005。
type GroupCoreService interface {
	GroupProfileService
	GroupMemberService
	GroupMemberManagementService
	GroupLifecycleService
	GroupDisbandService
}

// GroupService 保留为群业务的完整入口。
type GroupService interface {
	GroupCoreService
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
	UploadAvatar(context.Context, int64, string, io.Reader) (UploadResult, error)
}
