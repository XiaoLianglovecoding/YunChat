package model

import "time"

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
	Role       int        `json:"role"` // 0=成员, 1=管理员, 2=群主
	MutedUntil *time.Time `json:"muted_until,omitempty"`
	JoinedAt   time.Time  `json:"joined_at"`
}

// GroupAddedNotification / GroupRemovedNotification 与前端实时群变更契约一致。
type GroupAddedNotification struct {
	GroupID int64  `json:"groupId"`
	Name    string `json:"name"`
}

type GroupRemovedNotification struct {
	GroupID int64  `json:"groupId"`
	Reason  string `json:"reason"`
}
