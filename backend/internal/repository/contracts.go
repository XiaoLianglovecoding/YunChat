// Package repository 定义业务层依赖的数据端口，不包含任何数据库实现。
package repository

import (
	"context"
	"time"

	"my-im/internal/model"
)

// MySQLRepository 由 MySQLRepoImpl 实现；Service 只依赖此端口。
type MySQLRepository interface {
	// WithinTransaction 将回调内的所有 Repository 调用绑定到同一事务。
	// 回调返回错误时必须回滚；提交错误必须原样返回。
	WithinTransaction(context.Context, func(context.Context, MySQLRepository) error) error

	InsertPrivateMessage(context.Context, *model.PrivateMessage) error
	InsertGroupMessage(context.Context, *model.GroupMessage) error
	InsertMsgRevoked(context.Context, *model.MsgRevoked) error
	UpsertMessageUserState(context.Context, *model.MessageUserState) error

	GetUserByID(context.Context, int64) (*model.User, error)
	GetUserByUsername(context.Context, string) (*model.User, error)
	CreateUser(context.Context, *model.User) error
	UpdateUser(context.Context, *model.User) error
	UpdateUsername(context.Context, int64, string, string) error
	UpdatePasswordHash(context.Context, int64, string) error
	UpdateAvatarURL(context.Context, int64, string) error

	CreateFriendRequest(context.Context, *model.FriendRequest) error
	UpdateFriendRequest(context.Context, *model.FriendRequest) error
	GetFriendRequestByID(context.Context, int64) (*model.FriendRequest, error)
	GetFriendRequestsByUser(context.Context, int64) ([]model.FriendRequest, error)
	CreateFriendship(context.Context, *model.Friendship) error
	DeleteFriendship(context.Context, int64, int64) error
	GetFriendList(context.Context, int64) ([]model.Friendship, error)
	CountFriends(context.Context, int64) (int, error)
	IsFriend(context.Context, int64, int64) (bool, error)
	CreateBlacklist(context.Context, *model.Blacklist) error
	DeleteBlacklist(context.Context, int64, int64) error
	IsBlocked(context.Context, int64, int64) (bool, error)

	CreateGroup(context.Context, *model.Group) (int64, error)
	UpdateGroup(context.Context, *model.Group) error
	GetGroupByID(context.Context, int64) (*model.Group, error)
	AddGroupMember(context.Context, *model.GroupMember) error
	RemoveGroupMember(context.Context, int64, int64) error
	GetGroupMembers(context.Context, int64) ([]model.GroupMember, error)
	UpdateGroupMemberRole(context.Context, int64, int64, int) error

	CreateMoment(context.Context, *model.Moment) error
	GetMomentByID(context.Context, int64) (*model.Moment, error)
	DeleteMoment(context.Context, int64) error
	GetMomentsByIDs(context.Context, []int64) ([]model.Moment, error)
	GetMomentsByUser(context.Context, int64, int, int) ([]model.Moment, error)
	GetMomentLikers(context.Context, int64) ([]int64, error)
	BatchUpsertMomentLikes(context.Context, []model.MomentLike) error
	BatchDeleteMomentLikes(context.Context, []model.MomentLikeKey) error
	CreateMomentComment(context.Context, *model.MomentComment) error
	GetMomentCommentByID(context.Context, int64) (*model.MomentComment, error)
	GetMomentComments(context.Context, int64) ([]model.MomentComment, error)
	DeleteMomentComment(context.Context, int64) error

	GetUserSettings(context.Context, int64) (*model.UserSettings, error)
	CreateOrUpdateUserSettings(context.Context, *model.UserSettings) error
	SearchPrivateMessages(context.Context, int64, string, int, int) ([]model.PrivateMessage, error)
}

type PrivateMsgCheckResult struct {
	MessageID int64
	Timestamp int64
}

type GroupMsgCheckResult struct {
	MessageID int64
	GroupSeq  int64
	Timestamp int64
}

type RefreshSession struct {
	UserID    int64
	JTI       string
	FamilyID  string
	ExpiresAt time.Time
}

// RedisRepository 由 RedisRepoImpl 实现；键规范以 docs/DATABASE.md 为准。
type RedisRepository interface {
	StoreRefreshSession(context.Context, RefreshSession, time.Duration) error
	RotateRefreshSession(context.Context, string, RefreshSession, time.Duration) error
	RevokeUserRefreshSessions(context.Context, int64) error
	WriteInbox(context.Context, int64, *model.InboxMessage) error
	WriteOutbox(context.Context, int64, *model.InboxMessage) error
	ReadInbox(context.Context, int64, int64, int64, int) ([]model.InboxMessage, error)
	ReadOutbox(context.Context, int64, int64, int64, int) ([]model.InboxMessage, error)
	UpdateConvList(context.Context, int64, string, string, int64) error
	GetConvList(context.Context, int64) ([]model.ConvSummary, error)
	IncrementUnread(context.Context, int64, string) error
	ClearUnread(context.Context, int64, string) error
	GetUnreadMap(context.Context, int64) (map[string]int64, error)
	SetGroupReadPos(context.Context, int64, string, int64) error
	GetGroupReadPos(context.Context, int64, string) (int64, error)
	GetGroupMemberships(context.Context, int64) ([]int64, error)
	GetGroupMembers(context.Context, int64) ([]int64, error)
	AddGroupMember(context.Context, int64, int64) error
	RemoveGroupMember(context.Context, int64, int64) error
	ReplaceGroupMembers(context.Context, int64, []int64) error
	ReplaceUserGroups(context.Context, int64, []int64) error
	ExecPrivateMsgCheck(context.Context, int64, int64, string) (*PrivateMsgCheckResult, error)
	ExecGroupMsgCheck(context.Context, int64, int64, string) (*GroupMsgCheckResult, error)
	ExecInboxMarkRead(context.Context, int64, string) (int64, error)
	ExecRevokeMsg(context.Context, int64, string, int64, string, int64) (bool, error)
	FanoutMomentFeed(context.Context, []int64, int64, int64, int) error
	AddToMomentOutbox(context.Context, int64, int64, int64, int) error
	MarkBigUser(context.Context, int64) error
	FilterBigUsers(context.Context, []int64) ([]int64, error)
	GetTimelinePage(context.Context, int64, int64, int64, int) ([]model.FeedEntry, error)
	GetMomentOutboxPage(context.Context, int64, int64, int64, int) ([]model.FeedEntry, error)
	LikeMomentAtomic(context.Context, int64, int64) (bool, int64, error)
	UnlikeMomentAtomic(context.Context, int64, int64) (bool, int64, error)
	EnsureMomentLikesLoaded(context.Context, int64, func(context.Context) ([]int64, error), time.Duration) error
	GetMomentLikeStats(context.Context, int64, []int64) (map[int64]int64, map[int64]bool, error)
	GetMomentLikerIDs(context.Context, int64) ([]int64, error)
	DeleteMomentLikes(context.Context, int64) error
	SetFriendCache(context.Context, int64, int64) error
	DeleteFriendCache(context.Context, int64, int64) error
	ReplaceFriendCache(context.Context, int64, []int64) error
	SetBlacklistMember(context.Context, int64, int64) error
	DeleteBlacklistMember(context.Context, int64, int64) error
	ReplaceBlacklist(context.Context, int64, []int64) error
	SetGroupMemberInfo(context.Context, int64, int64, int, *time.Time) error
	DeleteGroupMemberInfo(context.Context, int64, int64) error
	ReplaceGroupMemberInfo(context.Context, int64, []model.GroupMember) error
}

// MessagePublisher 由 RabbitPublisher 实现，底层提供持久发布、confirm、mandatory 和有限重试。
type MessagePublisher interface {
	PublishPrivateMessage(context.Context, *model.PrivateMessage) error
	PublishGroupMessage(context.Context, *model.GroupMessage) error
	PublishMomentPush(context.Context, *model.Moment) error
	PublishLikeEvent(context.Context, *model.LikeEvent) error
}
