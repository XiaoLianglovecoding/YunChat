package handler

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/application"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
)

type userResponse struct {
	ID         string     `json:"id"`
	Username   string     `json:"username"`
	Email      string     `json:"email"`
	Phone      string     `json:"phone,omitempty"`
	Nickname   string     `json:"nickname"`
	AvatarURL  string     `json:"avatarUrl"`
	Bio        string     `json:"bio"`
	Status     uint8      `json:"status"`
	LastSeenAt *time.Time `json:"lastSeenAt"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type authSessionResponse struct {
	User         userResponse `json:"user"`
	AccessToken  string       `json:"accessToken"`
	RefreshToken string       `json:"refreshToken"`
	ExpiresIn    int64        `json:"expiresIn"`
}

type settingsResponse struct {
	UserID                string          `json:"userId"`
	Locale                string          `json:"locale"`
	Theme                 string          `json:"theme"`
	NotificationEnabled   bool            `json:"notificationEnabled"`
	MessagePreviewEnabled bool            `json:"messagePreviewEnabled"`
	Extra                 json.RawMessage `json:"extra,omitempty"`
	CreatedAt             time.Time       `json:"createdAt"`
	UpdatedAt             time.Time       `json:"updatedAt"`
}

func newUserResponse(user domain.User) userResponse {
	return userResponse{ID: strconv.FormatUint(uint64(user.ID), 10), Username: user.Username, Email: user.Email, Phone: user.Phone,
		Nickname: user.Nickname, AvatarURL: user.AvatarURL, Bio: user.Bio, Status: uint8(user.Status), LastSeenAt: user.LastSeenAt,
		CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt}
}
func newAuthSessionResponse(session application.AuthSession) authSessionResponse {
	return authSessionResponse{User: newUserResponse(session.User), AccessToken: session.AccessToken, RefreshToken: session.RefreshToken, ExpiresIn: session.ExpiresIn}
}
func newSettingsResponse(settings domain.UserSettings) settingsResponse {
	return settingsResponse{UserID: strconv.FormatUint(uint64(settings.UserID), 10), Locale: settings.Locale, Theme: settings.Theme, NotificationEnabled: settings.NotificationEnabled, MessagePreviewEnabled: settings.MessagePreviewEnabled, Extra: settings.Extra, CreatedAt: settings.CreatedAt, UpdatedAt: settings.UpdatedAt}
}
