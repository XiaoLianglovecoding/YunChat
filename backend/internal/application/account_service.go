package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/id"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/password"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/token"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/validate"
)

type Dependencies struct {
	Accounts     domain.AccountRepository
	IDs          *id.Generator
	Tokens       *token.Manager
	RefreshTTL   time.Duration
	Now          func() time.Time
	LoginLimiter RateLimiter
	LoginLimit   int
	LoginWindow  time.Duration
}

type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)
}

func NewServices(dependencies Dependencies) (Services, error) {
	if dependencies.Accounts == nil || dependencies.IDs == nil || dependencies.Tokens == nil {
		return Services{}, errors.New("account service dependencies are incomplete")
	}
	if dependencies.RefreshTTL <= 0 {
		return Services{}, errors.New("refresh token ttl must be positive")
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	account := &accountService{dependencies: dependencies}
	placeholder := todoService{}
	return Services{Auth: account, Users: account, Contacts: placeholder, Conversations: placeholder, Messages: placeholder}, nil
}

type accountService struct{ dependencies Dependencies }

func (service *accountService) Register(ctx context.Context, cmd RegisterCommand) (AuthSession, error) {
	username, email, nickname, err := normalizeRegistration(cmd)
	if err != nil {
		return AuthSession{}, err
	}
	hash, err := password.Hash(cmd.Password)
	if err != nil {
		return AuthSession{}, fmt.Errorf("hash password: %w", err)
	}
	userID, err := service.dependencies.IDs.Next()
	if err != nil {
		return AuthSession{}, fmt.Errorf("allocate user id: %w", err)
	}
	now := service.now().UTC()
	user := domain.User{ID: domain.UserID(userID), Username: username, Email: email, Nickname: nickname, Status: domain.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	settings := domain.UserSettings{UserID: domain.UserID(userID), Locale: "zh-CN", Theme: "system", NotificationEnabled: true, MessagePreviewEnabled: true, CreatedAt: now, UpdatedAt: now}
	device, session, refreshRaw, err := service.newSessionData(user.ID, LoginCommand{DeviceKey: cmd.DeviceKey, DeviceName: cmd.DeviceName, Platform: cmd.Platform}, now)
	if err != nil {
		return AuthSession{}, err
	}
	session, err = service.dependencies.Accounts.CreateAccountAndSession(ctx, user, hash, settings, device, session)
	if err != nil {
		return AuthSession{}, err
	}
	return service.finishSession(user, session, refreshRaw)
}

func (service *accountService) Login(ctx context.Context, cmd LoginCommand) (AuthSession, error) {
	login := strings.ToLower(strings.TrimSpace(cmd.Login))
	if login == "" || strings.TrimSpace(cmd.Password) == "" {
		return AuthSession{}, domain.ErrInvalidCredentials
	}
	if service.dependencies.LoginLimiter != nil {
		loginKey := fmt.Sprintf("login-account:%x", sha256.Sum256([]byte(login)))
		allowed, _, limitErr := service.dependencies.LoginLimiter.Allow(ctx, loginKey, service.dependencies.LoginLimit, service.dependencies.LoginWindow)
		if limitErr != nil {
			return AuthSession{}, limitErr
		}
		if !allowed {
			return AuthSession{}, domain.ErrRateLimited
		}
	}
	user, hash, err := service.dependencies.Accounts.FindByLogin(ctx, login)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return AuthSession{}, domain.ErrInvalidCredentials
		}
		return AuthSession{}, err
	}
	if user.Status == domain.UserStatusSuspended {
		return AuthSession{}, domain.ErrForbidden
	}
	if user.Status != domain.UserStatusActive || !password.Verify(hash, cmd.Password) {
		return AuthSession{}, domain.ErrInvalidCredentials
	}
	if strings.TrimSpace(cmd.DeviceKey) == "" {
		cmd.DeviceKey = "web-default"
	}
	if strings.TrimSpace(cmd.DeviceName) == "" {
		cmd.DeviceName = "LinkNest Web"
	}
	if strings.TrimSpace(cmd.Platform) == "" {
		cmd.Platform = "web"
	}
	return service.issueSession(ctx, user, cmd)
}

func (service *accountService) Refresh(ctx context.Context, rawToken string) (AuthSession, error) {
	rawToken = strings.TrimSpace(rawToken)
	if len(rawToken) < 32 {
		return AuthSession{}, domain.ErrUnauthorized
	}
	newRaw, newHash, err := newRefreshToken()
	if err != nil {
		return AuthSession{}, fmt.Errorf("create refresh token: %w", err)
	}
	tokenID, err := service.dependencies.IDs.Next()
	if err != nil {
		return AuthSession{}, fmt.Errorf("allocate refresh token id: %w", err)
	}
	replacement := domain.Session{TokenID: tokenID, TokenHash: newHash, ExpiresAt: service.now().UTC().Add(service.dependencies.RefreshTTL)}
	rotated, err := service.dependencies.Accounts.RotateRefreshToken(ctx, hashRefreshToken(rawToken), replacement, service.now().UTC())
	if err != nil {
		return AuthSession{}, err
	}
	user, err := service.dependencies.Accounts.FindByID(ctx, rotated.UserID)
	if err != nil {
		return AuthSession{}, err
	}
	accessToken, expiresAt, err := service.dependencies.Tokens.SignAccess(uint64(rotated.UserID), uint64(rotated.DeviceID), rotated.TokenID)
	if err != nil {
		return AuthSession{}, fmt.Errorf("sign access token: %w", err)
	}
	return AuthSession{User: user, TokenPair: TokenPair{AccessToken: accessToken, RefreshToken: newRaw, ExpiresIn: int64(time.Until(expiresAt).Seconds())}}, nil
}

func (service *accountService) Logout(ctx context.Context, userID domain.UserID, deviceID domain.DeviceID) error {
	if deviceID == 0 {
		return domain.ErrInvalidArgument
	}
	return service.dependencies.Accounts.RevokeDeviceSession(ctx, userID, deviceID, service.now().UTC())
}

func (service *accountService) ChangePassword(ctx context.Context, userID domain.UserID, currentPassword, newPassword string) error {
	currentHash, err := service.dependencies.Accounts.FindPasswordHash(ctx, userID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(currentPassword) == "" || !password.Verify(currentHash, currentPassword) {
		return domain.ErrInvalidCredentials
	}
	newHash, err := password.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return service.dependencies.Accounts.ChangePassword(ctx, userID, newHash)
}

func (service *accountService) GetMe(ctx context.Context, userID domain.UserID) (domain.User, error) {
	return service.dependencies.Accounts.FindByID(ctx, userID)
}

func (service *accountService) UpdateProfile(ctx context.Context, cmd UpdateProfileCommand) (domain.User, error) {
	user, err := service.GetMe(ctx, cmd.UserID)
	if err != nil {
		return domain.User{}, err
	}
	if cmd.Nickname != nil {
		if err := validate.Nickname(*cmd.Nickname); err != nil {
			return domain.User{}, fmt.Errorf("%w: %v", domain.ErrInvalidArgument, err)
		}
		user.Nickname = strings.TrimSpace(*cmd.Nickname)
	}
	if cmd.Email != nil {
		user.Email = strings.ToLower(strings.TrimSpace(*cmd.Email))
		if user.Email != "" {
			if err := validate.Email(user.Email); err != nil {
				return domain.User{}, fmt.Errorf("%w: %v", domain.ErrInvalidArgument, err)
			}
		}
	}
	if cmd.AvatarURL != nil {
		user.AvatarURL = strings.TrimSpace(*cmd.AvatarURL)
		if len(user.AvatarURL) > 512 {
			return domain.User{}, fmt.Errorf("%w: avatar url is too long", domain.ErrInvalidArgument)
		}
	}
	if cmd.Bio != nil {
		user.Bio = strings.TrimSpace(*cmd.Bio)
		if len(user.Bio) > 280 {
			return domain.User{}, fmt.Errorf("%w: bio is too long", domain.ErrInvalidArgument)
		}
	}
	user.UpdatedAt = service.now().UTC()
	if err := service.dependencies.Accounts.UpdateProfile(ctx, user); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (service *accountService) GetSettings(ctx context.Context, userID domain.UserID) (domain.UserSettings, error) {
	return service.dependencies.Accounts.GetSettings(ctx, userID)
}

func (service *accountService) UpdateSettings(ctx context.Context, cmd UpdateSettingsCommand) (domain.UserSettings, error) {
	settings, err := service.GetSettings(ctx, cmd.UserID)
	if err != nil {
		return domain.UserSettings{}, err
	}
	if cmd.Locale != nil {
		locale := strings.TrimSpace(*cmd.Locale)
		if locale != "zh-CN" && locale != "en-US" {
			return domain.UserSettings{}, fmt.Errorf("%w: unsupported locale", domain.ErrInvalidArgument)
		}
		settings.Locale = locale
	}
	if cmd.Theme != nil {
		theme := strings.TrimSpace(*cmd.Theme)
		if theme != "system" && theme != "light" && theme != "dark" {
			return domain.UserSettings{}, fmt.Errorf("%w: unsupported theme", domain.ErrInvalidArgument)
		}
		settings.Theme = theme
	}
	if cmd.NotificationEnabled != nil {
		settings.NotificationEnabled = *cmd.NotificationEnabled
	}
	if cmd.MessagePreviewEnabled != nil {
		settings.MessagePreviewEnabled = *cmd.MessagePreviewEnabled
	}
	if cmd.Extra != nil {
		settings.Extra = append([]byte(nil), cmd.Extra...)
	}
	settings.UpdatedAt = service.now().UTC()
	if err := service.dependencies.Accounts.UpdateSettings(ctx, settings); err != nil {
		return domain.UserSettings{}, err
	}
	return settings, nil
}

func (service *accountService) issueSession(ctx context.Context, user domain.User, cmd LoginCommand) (AuthSession, error) {
	now := service.now().UTC()
	device, sessionData, refreshRaw, err := service.newSessionData(user.ID, cmd, now)
	if err != nil {
		return AuthSession{}, err
	}
	session, err := service.dependencies.Accounts.StartSingleDeviceSession(ctx, user.ID, device, sessionData)
	if err != nil {
		return AuthSession{}, err
	}
	return service.finishSession(user, session, refreshRaw)
}

func (service *accountService) newSessionData(userID domain.UserID, cmd LoginCommand, now time.Time) (domain.Device, domain.Session, string, error) {
	if strings.TrimSpace(cmd.DeviceKey) == "" {
		cmd.DeviceKey = "web-default"
	}
	if strings.TrimSpace(cmd.DeviceName) == "" {
		cmd.DeviceName = "LinkNest Web"
	}
	if strings.TrimSpace(cmd.Platform) == "" {
		cmd.Platform = "web"
	}
	deviceID, err := service.dependencies.IDs.Next()
	if err != nil {
		return domain.Device{}, domain.Session{}, "", fmt.Errorf("allocate device id: %w", err)
	}
	tokenID, err := service.dependencies.IDs.Next()
	if err != nil {
		return domain.Device{}, domain.Session{}, "", fmt.Errorf("allocate refresh token id: %w", err)
	}
	familyID, err := service.dependencies.IDs.Next()
	if err != nil {
		return domain.Device{}, domain.Session{}, "", fmt.Errorf("allocate refresh token family id: %w", err)
	}
	refreshRaw, refreshHash, err := newRefreshToken()
	if err != nil {
		return domain.Device{}, domain.Session{}, "", fmt.Errorf("create refresh token: %w", err)
	}
	device := domain.Device{ID: domain.DeviceID(deviceID), UserID: userID, DeviceKey: cmd.DeviceKey, DeviceName: cmd.DeviceName, Platform: cmd.Platform, LastActiveAt: now}
	session := domain.Session{TokenID: tokenID, UserID: userID, DeviceID: domain.DeviceID(deviceID), FamilyID: familyID, TokenHash: refreshHash, ExpiresAt: now.Add(service.dependencies.RefreshTTL)}
	return device, session, refreshRaw, nil
}

func (service *accountService) finishSession(user domain.User, session domain.Session, refreshRaw string) (AuthSession, error) {
	accessToken, expiresAt, err := service.dependencies.Tokens.SignAccess(uint64(user.ID), uint64(session.DeviceID), session.TokenID)
	if err != nil {
		return AuthSession{}, fmt.Errorf("sign access token: %w", err)
	}
	return AuthSession{User: user, TokenPair: TokenPair{AccessToken: accessToken, RefreshToken: refreshRaw, ExpiresIn: int64(time.Until(expiresAt).Seconds())}}, nil
}

func normalizeRegistration(cmd RegisterCommand) (username, email, nickname string, err error) {
	username = strings.ToLower(strings.TrimSpace(cmd.Username))
	if err = validate.Username(username); err != nil {
		err = fmt.Errorf("%w: %v", domain.ErrInvalidArgument, err)
		return
	}
	nickname = strings.TrimSpace(cmd.Nickname)
	if err = validate.Nickname(nickname); err != nil {
		err = fmt.Errorf("%w: %v", domain.ErrInvalidArgument, err)
		return
	}
	email = strings.ToLower(strings.TrimSpace(cmd.Email))
	if email != "" {
		if err = validate.Email(email); err != nil {
			err = fmt.Errorf("%w: %v", domain.ErrInvalidArgument, err)
		}
	}
	return
}

func newRefreshToken() (string, []byte, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(buffer)
	return raw, hashRefreshToken(raw), nil
}
func hashRefreshToken(raw string) []byte       { hash := sha256.Sum256([]byte(raw)); return hash[:] }
func (service *accountService) now() time.Time { return service.dependencies.Now() }

var _ AuthUseCase = (*accountService)(nil)
var _ UserUseCase = (*accountService)(nil)
