package model

import "time"

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Nickname     string    `json:"nickname"`
	AvatarURL    string    `json:"avatar_url"`
	Sign         string    `json:"sign"`
	Gender       int       `json:"gender"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type FriendRequest struct {
	ID         int64     `json:"id"`
	FromUserID int64     `json:"from_user_id"`
	ToUserID   int64     `json:"to_user_id"`
	Username   string    `json:"username,omitempty"`
	AvatarURL  string    `json:"avatar_url,omitempty"`
	Message    string    `json:"message"`
	Status     int       `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

const (
	FriendRequestPending  = 0
	FriendRequestAccepted = 1
	FriendRequestRejected = 2
)

type Friendship struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	FriendID  int64     `json:"friend_id"`
	Nickname  string    `json:"nickname,omitempty"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Online    bool      `json:"online"`
	IsBlocked bool      `json:"is_blocked"`
	CreatedAt time.Time `json:"created_at"`
}

type Blacklist struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	BlockedID int64     `json:"blocked_id"`
	CreatedAt time.Time `json:"created_at"`
}
