package handler

import (
	"encoding/json"
	"errors"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/application"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/realtime"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/httpx"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/middleware"
	"github.com/gin-gonic/gin"
)

type Set struct {
	services application.Services
	hub      *realtime.Hub
}

func New(services application.Services, hub *realtime.Hub) *Set {
	return &Set{services: services, hub: hub}
}

func (set *Set) Todo(useCase string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// TODO(linknest): bind transport data and invoke the matching application service.
		_ = set.services
		httpx.NotImplemented(ctx, useCase)
	}
}

type registerRequest struct {
	Username   string `json:"username"`
	Email      string `json:"email"`
	Password   string `json:"password"`
	Nickname   string `json:"nickname"`
	DeviceKey  string `json:"deviceKey"`
	DeviceName string `json:"deviceName"`
	Platform   string `json:"platform"`
}

type loginRequest struct {
	Login      string `json:"login"`
	Password   string `json:"password"`
	DeviceKey  string `json:"deviceKey"`
	DeviceName string `json:"deviceName"`
	Platform   string `json:"platform"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}
type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (set *Set) Register(ctx *gin.Context) {
	var request registerRequest
	if err := bindJSON(ctx, &request); err != nil {
		httpx.Error(ctx, err)
		return
	}
	session, err := set.services.Auth.Register(ctx, application.RegisterCommand{Username: request.Username, Email: request.Email, Password: request.Password, Nickname: request.Nickname, DeviceKey: request.DeviceKey, DeviceName: request.DeviceName, Platform: request.Platform})
	if err != nil {
		httpx.Error(ctx, err)
		return
	}
	httpx.Created(ctx, newAuthSessionResponse(session))
}

func (set *Set) Login(ctx *gin.Context) {
	var request loginRequest
	if err := bindJSON(ctx, &request); err != nil {
		httpx.Error(ctx, err)
		return
	}
	session, err := set.services.Auth.Login(ctx, application.LoginCommand{Login: request.Login, Password: request.Password, DeviceKey: request.DeviceKey, DeviceName: request.DeviceName, Platform: request.Platform})
	if err != nil {
		httpx.Error(ctx, err)
		return
	}
	httpx.OK(ctx, newAuthSessionResponse(session))
}

func (set *Set) Refresh(ctx *gin.Context) {
	var request refreshRequest
	if err := bindJSON(ctx, &request); err != nil {
		httpx.Error(ctx, err)
		return
	}
	session, err := set.services.Auth.Refresh(ctx, request.RefreshToken)
	if err != nil {
		httpx.Error(ctx, err)
		return
	}
	httpx.OK(ctx, newAuthSessionResponse(session))
}

func (set *Set) Logout(ctx *gin.Context) {
	principal, ok := middleware.CurrentPrincipal(ctx)
	if !ok {
		httpx.Error(ctx, domain.ErrUnauthorized)
		return
	}
	if err := set.services.Auth.Logout(ctx, principal.UserID, principal.DeviceID); err != nil {
		httpx.Error(ctx, err)
		return
	}
	httpx.OK(ctx, nil)
}

func (set *Set) ChangePassword(ctx *gin.Context) {
	principal, ok := middleware.CurrentPrincipal(ctx)
	if !ok {
		httpx.Error(ctx, domain.ErrUnauthorized)
		return
	}
	var request changePasswordRequest
	if err := bindJSON(ctx, &request); err != nil {
		httpx.Error(ctx, err)
		return
	}
	if err := set.services.Auth.ChangePassword(ctx, principal.UserID, request.CurrentPassword, request.NewPassword); err != nil {
		httpx.Error(ctx, err)
		return
	}
	httpx.OK(ctx, nil)
}

func (set *Set) GetMe(ctx *gin.Context) {
	principal, ok := middleware.CurrentPrincipal(ctx)
	if !ok {
		httpx.Error(ctx, domain.ErrUnauthorized)
		return
	}
	user, err := set.services.Users.GetMe(ctx, principal.UserID)
	if err != nil {
		httpx.Error(ctx, err)
		return
	}
	httpx.OK(ctx, newUserResponse(user))
}

type updateProfileRequest struct {
	Nickname  *string `json:"nickname"`
	Email     *string `json:"email"`
	AvatarURL *string `json:"avatarUrl"`
	Bio       *string `json:"bio"`
}

func (set *Set) UpdateMe(ctx *gin.Context) {
	principal, ok := middleware.CurrentPrincipal(ctx)
	if !ok {
		httpx.Error(ctx, domain.ErrUnauthorized)
		return
	}
	var request updateProfileRequest
	if err := bindJSON(ctx, &request); err != nil {
		httpx.Error(ctx, err)
		return
	}
	user, err := set.services.Users.UpdateProfile(ctx, application.UpdateProfileCommand{UserID: principal.UserID, Nickname: request.Nickname, Email: request.Email, AvatarURL: request.AvatarURL, Bio: request.Bio})
	if err != nil {
		httpx.Error(ctx, err)
		return
	}
	httpx.OK(ctx, newUserResponse(user))
}

func (set *Set) GetSettings(ctx *gin.Context) {
	principal, ok := middleware.CurrentPrincipal(ctx)
	if !ok {
		httpx.Error(ctx, domain.ErrUnauthorized)
		return
	}
	settings, err := set.services.Users.GetSettings(ctx, principal.UserID)
	if err != nil {
		httpx.Error(ctx, err)
		return
	}
	httpx.OK(ctx, newSettingsResponse(settings))
}

type updateSettingsRequest struct {
	Locale                *string         `json:"locale"`
	Theme                 *string         `json:"theme"`
	NotificationEnabled   *bool           `json:"notificationEnabled"`
	MessagePreviewEnabled *bool           `json:"messagePreviewEnabled"`
	Extra                 json.RawMessage `json:"extra"`
}

func (set *Set) UpdateSettings(ctx *gin.Context) {
	principal, ok := middleware.CurrentPrincipal(ctx)
	if !ok {
		httpx.Error(ctx, domain.ErrUnauthorized)
		return
	}
	var request updateSettingsRequest
	if err := bindJSON(ctx, &request); err != nil {
		httpx.Error(ctx, err)
		return
	}
	settings, err := set.services.Users.UpdateSettings(ctx, application.UpdateSettingsCommand{UserID: principal.UserID, Locale: request.Locale, Theme: request.Theme, NotificationEnabled: request.NotificationEnabled, MessagePreviewEnabled: request.MessagePreviewEnabled, Extra: request.Extra})
	if err != nil {
		httpx.Error(ctx, err)
		return
	}
	httpx.OK(ctx, newSettingsResponse(settings))
}

func bindJSON(ctx *gin.Context, target any) error {
	if ctx.Request.Body == nil {
		return domain.ErrInvalidArgument
	}
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.Join(domain.ErrInvalidArgument, err)
	}
	return nil
}

func (set *Set) Health(ctx *gin.Context) {
	httpx.OK(ctx, gin.H{
		"status":             "ok",
		"service":            "linknest-api",
		"realtime_clients":   set.hub.ConnectionCount(),
		"business_readiness": "m1-account",
	})
}

func (set *Set) Ready(ctx *gin.Context) {
	httpx.OK(ctx, gin.H{"status": "ready", "dependencies": "mysql-redis-checked-at-startup"})
}
