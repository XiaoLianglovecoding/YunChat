package model

import "time"

type Moment struct {
	ID         int64     `json:"id"`
	AuthorID   int64     `json:"author_id"`
	Content    string    `json:"content"`
	MediaUrls  *string   `json:"media_urls,omitempty"` // 以JSON字符串形式存储在数据库中；可为空
	Visibility int       `json:"visibility"`           // 2=好友可见，3=仅自己可见（1 为历史值，读取时按好友可见处理）
	CreatedAt  time.Time `json:"created_at"`

	// 以下字段不落库，仅在读取时由点赞缓存（Redis）填充：
	LikeCount    int64           `json:"like_count"`  // 点赞数
	LikedByMe    bool            `json:"liked_by_me"` // 当前查看者是否已赞
	AuthorName   string          `json:"author_name"`
	AuthorAvatar string          `json:"author_avatar"`
	Comments     []MomentComment `json:"comments"`
}

type MomentLike struct {
	ID        int64     `json:"id"`
	MomentID  int64     `json:"moment_id"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// MomentLiker 是动态点赞列表中可展示的用户资料。
type MomentLiker struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
}

// MomentLikeKey 是点赞明细的联合主键 (momentID,userID)，用于批量删除。
type MomentLikeKey struct {
	MomentID int64
	UserID   int64
}

// LikeEvent 是点赞/取消赞的持久化事件，经 like_persist 队列异步批量写入 MySQL。
type LikeEvent struct {
	MomentID int64  `json:"moment_id"`
	UserID   int64  `json:"user_id"`
	Action   string `json:"action"` // "like" | "unlike"
	Ts       int64  `json:"ts"`     // 事件时间戳（毫秒），用作 moment_likes.created_at
}

// 点赞事件动作常量。
const (
	LikeActionLike   = "like"
	LikeActionUnlike = "unlike"
)

type MomentComment struct {
	ID        int64     `json:"id"`
	MomentID  int64     `json:"moment_id"`
	UserID    int64     `json:"user_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	Username  string    `json:"username"`
	AvatarURL string    `json:"avatar_url"`
}

// FeedEntry 是 Feed ZSet（收件箱/寄件箱）中的一条记录：
// 动态 ID 及其发布时间戳（毫秒），用于推拉合并时的排序与游标分页。
type FeedEntry struct {
	MomentID int64
	Ts       int64 // 发布时间戳（毫秒），即 ZSet 的 score
}
