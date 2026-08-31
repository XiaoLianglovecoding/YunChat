// Package auth 负责 JWT 的签发与校验，不包含 HTTP 或数据库逻辑。
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

var ErrInvalidToken = errors.New("invalid token")

// Claims 是 MyIM 冻结后的 JWT 载荷。TokenType 防止把 refresh token 当成 access token 使用。
type Claims struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	TokenType string `json:"token_type"`
	FamilyID  string `json:"family_id,omitempty"`
	jwt.RegisteredClaims
}

// Pair 除了返回给客户端的两个令牌，还带有服务端写 Redis 所需的刷新会话元数据。
type Pair struct {
	AccessToken     string
	RefreshToken    string
	AccessExpiresIn int64
	RefreshJTI      string
	FamilyID        string
	RefreshExpires  time.Time
}

type Manager struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

func NewManager(secret, issuer string, accessTTL, refreshTTL time.Duration) (*Manager, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT secret must contain at least 32 characters")
	}
	if strings.TrimSpace(issuer) == "" {
		return nil, errors.New("JWT issuer must not be empty")
	}
	if accessTTL <= 0 || refreshTTL <= 0 {
		return nil, errors.New("JWT expiration durations must be positive")
	}
	return &Manager{secret: []byte(secret), issuer: issuer, accessTTL: accessTTL, refreshTTL: refreshTTL, now: time.Now}, nil
}

// IssuePair 签发一对令牌。familyID 为空表示一次全新的登录，否则表示刷新时沿用令牌家族。
func (m *Manager) IssuePair(userID int64, username, familyID string) (Pair, error) {
	if userID <= 0 || strings.TrimSpace(username) == "" {
		return Pair{}, errors.New("token identity is incomplete")
	}
	if familyID == "" {
		var err error
		familyID, err = randomID()
		if err != nil {
			return Pair{}, fmt.Errorf("generate token family id: %w", err)
		}
	}
	accessJTI, err := randomID()
	if err != nil {
		return Pair{}, fmt.Errorf("generate access jti: %w", err)
	}
	refreshJTI, err := randomID()
	if err != nil {
		return Pair{}, fmt.Errorf("generate refresh jti: %w", err)
	}

	now := m.now().UTC()
	accessExpires := now.Add(m.accessTTL)
	refreshExpires := now.Add(m.refreshTTL)
	access, err := m.sign(Claims{
		UserID: userID, Username: username, TokenType: TokenTypeAccess,
		RegisteredClaims: registered(m.issuer, userID, accessJTI, now, accessExpires),
	})
	if err != nil {
		return Pair{}, fmt.Errorf("sign access token: %w", err)
	}
	refresh, err := m.sign(Claims{
		UserID: userID, Username: username, TokenType: TokenTypeRefresh, FamilyID: familyID,
		RegisteredClaims: registered(m.issuer, userID, refreshJTI, now, refreshExpires),
	})
	if err != nil {
		return Pair{}, fmt.Errorf("sign refresh token: %w", err)
	}
	return Pair{
		AccessToken: access, RefreshToken: refresh, AccessExpiresIn: int64(m.accessTTL.Seconds()),
		RefreshJTI: refreshJTI, FamilyID: familyID, RefreshExpires: refreshExpires,
	}, nil
}

func (m *Manager) ParseAccess(raw string) (*Claims, error) {
	return m.parse(raw, TokenTypeAccess)
}

func (m *Manager) ParseRefresh(raw string) (*Claims, error) {
	return m.parse(raw, TokenTypeRefresh)
}

func (m *Manager) sign(claims Claims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

func (m *Manager) parse(raw, expectedType string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(m.issuer),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(m.now))
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.TokenType != expectedType || claims.UserID <= 0 || strings.TrimSpace(claims.Username) == "" || claims.ID == "" {
		return nil, ErrInvalidToken
	}
	if claims.Subject != strconv.FormatInt(claims.UserID, 10) {
		return nil, ErrInvalidToken
	}
	if expectedType == TokenTypeRefresh && claims.FamilyID == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func registered(issuer string, userID int64, jti string, issuedAt, expiresAt time.Time) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Issuer: issuer, Subject: strconv.FormatInt(userID, 10), ID: jti,
		IssuedAt: jwt.NewNumericDate(issuedAt), ExpiresAt: jwt.NewNumericDate(expiresAt),
	}
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
