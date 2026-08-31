package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"my-im/internal/apperror"
	authtoken "my-im/internal/auth"
	"my-im/internal/model"
	"my-im/internal/repository"
)

// authUserRepository 是账户模块真正需要的最小数据库能力，便于隔离测试。
type authUserRepository interface {
	GetUserByID(context.Context, int64) (*model.User, error)
	GetUserByUsername(context.Context, string) (*model.User, error)
	CreateUser(context.Context, *model.User) error
	UpdateUsername(context.Context, int64, string, string) error
	UpdatePasswordHash(context.Context, int64, string) error
}

type refreshSessionRepository interface {
	StoreRefreshSession(context.Context, repository.RefreshSession, timeDuration) error
	RotateRefreshSession(context.Context, string, repository.RefreshSession, timeDuration) error
	RevokeUserRefreshSessions(context.Context, int64) error
}

// timeDuration 是 time.Duration 的别名；窄接口因此仍与 repository 实现完全匹配。
type timeDuration = time.Duration

type AuthServiceImpl struct {
	users     authUserRepository
	sessions  refreshSessionRepository
	tokens    *authtoken.Manager
	dummyHash []byte
	logger    *zap.Logger
}

func NewAuthService(users authUserRepository, sessions refreshSessionRepository, tokens *authtoken.Manager, logger *zap.Logger) (*AuthServiceImpl, error) {
	if users == nil || sessions == nil || tokens == nil {
		return nil, errors.New("auth service dependencies must not be nil")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	// 未知用户名也执行一次真实 bcrypt 比较，使登录失败路径不会直接暴露“用户不存在”。
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("my-im-dummy-password"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &AuthServiceImpl{users: users, sessions: sessions, tokens: tokens, dummyHash: dummyHash, logger: logger}, nil
}

func (s *AuthServiceImpl) Register(ctx context.Context, command RegisterCommand) (RegisterResult, error) {
	username, err := validateUsername(command.Username)
	if err != nil {
		return RegisterResult{}, err
	}
	if err := validatePassword(command.Password); err != nil {
		return RegisterResult{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(command.Password), bcrypt.DefaultCost)
	if err != nil {
		return RegisterResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	user := &model.User{Username: username, PasswordHash: string(hash), Nickname: username}
	if err := s.users.CreateUser(ctx, user); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return RegisterResult{}, apperror.New(apperror.CodeUsernameTaken)
		}
		return RegisterResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	return RegisterResult{UserID: user.ID, Username: user.Username}, nil
}

func (s *AuthServiceImpl) Login(ctx context.Context, command LoginCommand) (TokenPair, error) {
	user, findErr := s.users.GetUserByUsername(ctx, strings.TrimSpace(command.Username))
	if findErr != nil || user == nil {
		// 无论是不存在还是查询失败，都先走同一种高成本密码算法路径。
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(command.Password))
		if findErr != nil {
			return TokenPair{}, apperror.Wrap(apperror.CodeInternalFailure, findErr)
		}
		return TokenPair{}, apperror.New(apperror.CodeWrongPassword)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(command.Password)); err != nil {
		return TokenPair{}, apperror.New(apperror.CodeWrongPassword)
	}
	return s.issueAndStore(ctx, user, "")
}

func (s *AuthServiceImpl) Refresh(ctx context.Context, rawRefreshToken string) (TokenPair, error) {
	claims, err := s.tokens.ParseRefresh(rawRefreshToken)
	if err != nil {
		return TokenPair{}, apperror.New(apperror.CodeInvalidToken)
	}
	user, err := s.users.GetUserByID(ctx, claims.UserID)
	if err != nil {
		return TokenPair{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	if user == nil {
		return TokenPair{}, apperror.New(apperror.CodeInvalidToken)
	}
	pair, session, ttl, err := s.newPair(user, claims.FamilyID)
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.sessions.RotateRefreshSession(ctx, claims.ID, session, ttl); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return TokenPair{}, apperror.New(apperror.CodeInvalidToken)
		}
		return TokenPair{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	return pair, nil
}

func (s *AuthServiceImpl) UpdateUsername(ctx context.Context, userID int64, rawUsername string) (TokenPair, error) {
	username, err := validateUsername(rawUsername)
	if err != nil {
		return TokenPair{}, err
	}
	user, err := s.users.GetUserByID(ctx, userID)
	if err != nil {
		return TokenPair{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	if user == nil {
		return TokenPair{}, apperror.New(apperror.CodeUserNotFound)
	}
	oldUsername := user.Username
	if username != oldUsername {
		if err := s.users.UpdateUsername(ctx, userID, oldUsername, username); err != nil {
			if errors.Is(err, repository.ErrConflict) {
				return TokenPair{}, apperror.New(apperror.CodeUsernameTaken)
			}
			if errors.Is(err, repository.ErrNotFound) {
				return TokenPair{}, apperror.New(apperror.CodeUserNotFound)
			}
			return TokenPair{}, apperror.Wrap(apperror.CodeInternalFailure, err)
		}
	}
	user.Username = username
	if user.Nickname == oldUsername {
		user.Nickname = username
	}
	// 用户名已经进入 JWT，所以旧刷新会话必须撤销，再签发含新用户名的一对令牌。
	if err := s.sessions.RevokeUserRefreshSessions(ctx, userID); err != nil {
		return TokenPair{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	return s.issueAndStore(ctx, user, "")
}

func (s *AuthServiceImpl) UpdatePassword(ctx context.Context, userID int64, currentPassword, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	user, err := s.users.GetUserByID(ctx, userID)
	if err != nil {
		return apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	if user == nil {
		return apperror.New(apperror.CodeUserNotFound)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		return apperror.New(apperror.CodeWrongPassword)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	// 先撤销 Redis 会话、再落密码：Redis 故障时旧密码仍可用，但绝不会出现“密码已改、旧刷新令牌仍有效”。
	if err := s.sessions.RevokeUserRefreshSessions(ctx, userID); err != nil {
		return apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	if err := s.users.UpdatePasswordHash(ctx, userID, string(hash)); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apperror.New(apperror.CodeUserNotFound)
		}
		return apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	// 审计只记录事件和主体，不记录旧密码、新密码或任何 Token。
	s.logger.Info("security_event", zap.String("event", "password_changed"), zap.Int64("user_id", userID))
	return nil
}

func (s *AuthServiceImpl) issueAndStore(ctx context.Context, user *model.User, familyID string) (TokenPair, error) {
	pair, session, ttl, err := s.newPair(user, familyID)
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.sessions.StoreRefreshSession(ctx, session, ttl); err != nil {
		return TokenPair{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	return pair, nil
}

func (s *AuthServiceImpl) newPair(user *model.User, familyID string) (TokenPair, repository.RefreshSession, time.Duration, error) {
	issued, err := s.tokens.IssuePair(user.ID, user.Username, familyID)
	if err != nil {
		return TokenPair{}, repository.RefreshSession{}, 0, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	session := repository.RefreshSession{UserID: user.ID, JTI: issued.RefreshJTI, FamilyID: issued.FamilyID, ExpiresAt: issued.RefreshExpires}
	return TokenPair{AccessToken: issued.AccessToken, RefreshToken: issued.RefreshToken, ExpiresIn: issued.AccessExpiresIn, AvatarURL: user.AvatarURL}, session, time.Until(issued.RefreshExpires), nil
}

func validateUsername(raw string) (string, error) {
	username := strings.TrimSpace(raw)
	count := utf8.RuneCountInString(username)
	if username != raw || count < 3 || count > 50 {
		return "", apperror.New(apperror.CodeUsernameTooShort)
	}
	for _, r := range username {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", apperror.WithMessage(apperror.CodeInvalidParam, "username must not contain whitespace or control characters")
		}
	}
	return username, nil
}

func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < 6 {
		return apperror.New(apperror.CodePasswordTooShort)
	}
	if len([]byte(password)) > 72 {
		return apperror.WithMessage(apperror.CodeInvalidParam, "password must not exceed 72 bytes")
	}
	return nil
}
