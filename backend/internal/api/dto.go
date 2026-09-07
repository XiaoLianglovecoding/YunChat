package api

import "time"

// 本文件只固化 HTTP 传输契约，不包含业务逻辑。

type RegisterRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterResponse struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
}

type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	AvatarURL    string `json:"avatar_url,omitempty"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type UpdateUsernameRequest struct {
	Username string `json:"username" binding:"required"`
}

type UpdatePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

type SendFriendRequest struct {
	ToUserID int64  `json:"to_user_id" binding:"required"`
	Message  string `json:"message"`
}

type SendFriendResponse struct {
	RequestID  int64 `json:"request_id"`
	FromUserID int64 `json:"from_user_id"`
	ToUserID   int64 `json:"to_user_id"`
	Status     int   `json:"status"`
}

type FriendRequestAction struct {
	RequestID int64 `json:"request_id" binding:"required"`
}

type BlockUserRequest struct {
	BlockedID int64 `json:"blocked_id" binding:"required"`
}

type PaginationResponse struct {
	Total   int64 `json:"total"`
	Offset  int   `json:"offset"`
	Limit   int   `json:"limit"`
	HasMore bool  `json:"has_more"`
}

type PageResponse[T any] struct {
	Items      []T                `json:"items"`
	Pagination PaginationResponse `json:"pagination"`
}

type CreateGroupRequest struct {
	Name   string `json:"name" binding:"required"`
	Notice string `json:"notice"`
}

type CreateGroupResponse struct {
	GroupID int64 `json:"group_id"`
}

type UpdateGroupRequest struct {
	Name   string `json:"name" binding:"required"`
	Notice string `json:"notice"`
}

type AddGroupMemberRequest struct {
	MemberID int64 `json:"member_id" binding:"required"`
}

type UpdateGroupMemberRoleRequest struct {
	Role *int `json:"role" binding:"required"`
}

type MuteGroupMemberRequest struct {
	MutedUntil *time.Time `json:"muted_until" binding:"required"`
}

type TransferGroupOwnerRequest struct {
	NewOwnerID int64 `json:"new_owner_id" binding:"required"`
}

type PublishMomentRequest struct {
	Content    string  `json:"content" binding:"required"`
	MediaURLs  *string `json:"media_urls,omitempty"`
	Visibility int     `json:"visibility" binding:"required"`
}

type MomentActionResponse struct {
	OK    bool  `json:"ok"`
	Liked bool  `json:"liked"`
	Count int64 `json:"count"`
}

type CommentMomentRequest struct {
	Content string `json:"content" binding:"required"`
}

type UpdateSettingsRequest struct {
	NotificationEnabled bool   `json:"notification_enabled"`
	MessagePreview      bool   `json:"msg_preview_enabled"`
	MuteList            string `json:"mute_list"`
}

type MuteConversationRequest struct {
	ConvID string `json:"convId" binding:"required"`
}

type RevokeMessageRequest struct {
	ConvID string `json:"convId" binding:"required"`
	MsgID  int64  `json:"msgId" binding:"required"`
}

type UploadAvatarResponse struct {
	URL      string `json:"url"`
	FilePath string `json:"file_path"`
	Size     int64  `json:"size"`
}
