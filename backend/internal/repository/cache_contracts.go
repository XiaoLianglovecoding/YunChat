package repository

import (
	"context"
	"time"

	"my-im/internal/model"
)

// CacheResource identifies one independently rebuildable Redis projection.
// ResourceID is a user ID for friends/blacklist and a group ID for group_members.
type CacheResource string

const (
	CacheResourceFriends      CacheResource = "friends"
	CacheResourceBlacklist    CacheResource = "blacklist"
	CacheResourceGroupMembers CacheResource = "group_members"
)

func (r CacheResource) Valid() bool {
	switch r {
	case CacheResourceFriends, CacheResourceBlacklist, CacheResourceGroupMembers:
		return true
	default:
		return false
	}
}

// CacheReconcileEvent asks a worker to reread current MySQL truth. It deliberately
// does not contain an old SET/DEL operation, so delayed delivery is harmless.
type CacheReconcileEvent struct {
	ID           int64
	ResourceType CacheResource
	ResourceID   int64
	Attempts     int
	LockToken    string
}

// CacheEventWriter is intentionally tiny so a domain transaction can enqueue a
// reconciliation request without depending on the whole cache subsystem.
type CacheEventWriter interface {
	EnqueueCacheReconcile(context.Context, CacheResource, int64) error
}

// CacheSnapshotRepository is the read-only MySQL view used while an owner row
// is locked. Keeping it narrow makes the ordering contract explicit.
type CacheSnapshotRepository interface {
	ListFriendIDsForCache(context.Context, int64) ([]int64, error)
	ListBlockedIDsForCache(context.Context, int64) ([]int64, error)
	ListGroupMembersForCache(context.Context, int64) ([]model.GroupMember, error)
}

// CacheTruthRepository reads authoritative relationship state from MySQL and
// manages the durable reconciliation queue.
type CacheTruthRepository interface {
	CacheEventWriter
	CacheSnapshotRepository
	WithinCacheSnapshot(context.Context, CacheResource, int64, func(context.Context, CacheSnapshotRepository) error) error
	ListUserIDs(context.Context, int64, int) ([]int64, error)
	ListGroupIDs(context.Context, int64, int) ([]int64, error)
	ClaimCacheReconcileEvents(context.Context, string, int, time.Duration) ([]CacheReconcileEvent, error)
	MarkCacheReconcileSuccess(context.Context, int64, string) error
	MarkCacheReconcileFailure(context.Context, int64, string, error, time.Duration) error
}

type RelationshipCacheSnapshot struct {
	Loaded bool
	IDs    []int64
}

type GroupMemberCacheSnapshot struct {
	Loaded               bool
	Members              []model.GroupMember
	MissingSetIDs        []int64 // Hash has metadata, but group_members Set lacks the user.
	MissingInfoIDs       []int64 // Set has the user, but group_member_info Hash lacks metadata.
	MissingReverseIDs    []int64 // group_members 有成员，但 user_groups:{uid} 缺少本群。
	UnexpectedReverseIDs []int64 // user_groups:{uid} 有本群，但 group_members 没有此成员。
}

// RelationshipCacheRepository owns only Redis's rebuildable relationship
// projection. Missing/failed Redis data is never interpreted as MySQL truth.
type RelationshipCacheRepository interface {
	FriendsLoaded(context.Context, int64) (bool, error)
	BlacklistLoaded(context.Context, int64) (bool, error)
	GroupMembersLoaded(context.Context, int64) (bool, error)
	// InvalidateProjectionMarkers prepares an operator-requested strict rebuild.
	// It clears both the business loaded marker and any bounded owner-index
	// migration marker, so the following replacement also discovers legacy or
	// manually-created keys that are missing from the owner index.
	InvalidateProjectionMarkers(context.Context, CacheResource, int64) error
	TryCacheWarmLock(context.Context, CacheResource, int64, time.Duration) (string, bool, error)
	ReleaseCacheWarmLock(context.Context, CacheResource, int64, string) error
	ReplaceFriendOwner(context.Context, int64, []int64) error
	ReplaceBlacklistOwner(context.Context, int64, []int64) error
	ReplaceGroupMembersOwner(context.Context, int64, []model.GroupMember) error
	ReadFriendSnapshot(context.Context, int64) (RelationshipCacheSnapshot, error)
	ReadBlacklistSnapshot(context.Context, int64) (RelationshipCacheSnapshot, error)
	ReadGroupMemberSnapshot(context.Context, int64) (GroupMemberCacheSnapshot, error)
	GetOnlineStates(context.Context, []int64) (map[int64]bool, error)
}
