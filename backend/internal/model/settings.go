package model

import "time"

// UserSettings 存储每个用户的通知和对话偏好设置。
type UserSettings struct {
	ID                  int64     `json:"id"`
	UserID              int64     `json:"user_id"`
	NotificationEnabled bool      `json:"notification_enabled"`
	MsgPreviewEnabled   bool      `json:"msg_preview_enabled"`
	MuteList            string    `json:"mute_list"` // 已静音会话ID的JSON数组；空字符串表示null
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}
