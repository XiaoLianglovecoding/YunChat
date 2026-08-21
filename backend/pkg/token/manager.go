package token

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID    uint64 `json:"uid"`
	DeviceID  uint64 `json:"did"`
	SessionID uint64 `json:"sid"`
	jwt.RegisteredClaims
}

type Manager struct {
	issuer    string
	secret    []byte
	accessTTL time.Duration
	now       func() time.Time
}

func NewManager(issuer, secret string, accessTTL time.Duration) (*Manager, error) {
	if issuer == "" {
		return nil, errors.New("token issuer is required")
	}
	if len(secret) < 32 {
		return nil, errors.New("token secret must contain at least 32 characters")
	}
	if accessTTL <= 0 {
		return nil, errors.New("access token ttl must be positive")
	}
	return &Manager{issuer: issuer, secret: []byte(secret), accessTTL: accessTTL, now: time.Now}, nil
}

func (m *Manager) SignAccess(userID, deviceID, sessionID uint64) (string, time.Time, error) {
	now := m.now().UTC()
	expiresAt := now.Add(m.accessTTL)
	claims := Claims{
		UserID: userID, DeviceID: deviceID, SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   strconv.FormatUint(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	return signed, expiresAt, err
}

func (m *Manager) ParseAccess(raw string) (Claims, error) {
	claims := Claims{}
	parsed, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		return Claims{}, errors.New("invalid access token")
	}
	return claims, nil
}
