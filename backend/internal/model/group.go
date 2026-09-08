package model

import "time"

const (
	GroupRoleMember = iota
	GroupRoleAdmin
	GroupRoleOwner
)

const (
	GroupRemovedReasonLeft             = "left"
	GroupRemovedReasonDissolved        = "dissolved"
	GroupUpdatedReasonOwnerTransferred = "owner_transferred"
	GroupUpdatedReasonMemberLeft       = "member_left"
)

type Group struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Notice     string    `json:"notice"`
	OwnerID    int64     `json:"owner_id"`
	MaxMembers int       `json:"max_members"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type GroupMember struct {
	ID         int64      `json:"id"`
	GroupID    int64      `json:"group_id"`
	UserID     int64      `json:"user_id"`
	Role       int        `json:"role"` // GroupRoleMember/Admin/Owner
	MutedUntil *time.Time `json:"muted_until,omitempty"`
	JoinedAt   time.Time  `json:"joined_at"`
}

// GroupAddedNotification / GroupRemovedNotification / GroupUpdatedNotification
// 与前端实时群变更契约一致。
type GroupAddedNotification struct {
	GroupID int64  `json:"groupId"`
	Name    string `json:"name"`
}

type GroupRemovedNotification struct {
	GroupID int64  `json:"groupId"`
	Reason  string `json:"reason"`
}

// GroupUpdatedNotification is a lightweight refresh hint. It intentionally
// carries no role snapshot: clients reread the authoritative group/member
// endpoints, avoiding stale authorization data in a best-effort WS frame.
type GroupUpdatedNotification struct {
	GroupID int64  `json:"groupId"`
	Reason  string `json:"reason"`
}
