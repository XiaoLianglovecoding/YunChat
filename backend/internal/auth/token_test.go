package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestManagerIssuesTypedTokenPair(t *testing.T) {
	manager, err := NewManager(testSecret, "my-im-test", 2*time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := manager.IssuePair(42, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	access, err := manager.ParseAccess(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if access.UserID != 42 || access.Username != "alice" || access.Issuer != "my-im-test" || access.ExpiresAt == nil {
		t.Fatalf("unexpected access claims: %+v", access)
	}
	refresh, err := manager.ParseRefresh(pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if refresh.ID != pair.RefreshJTI || refresh.FamilyID == "" || refresh.FamilyID != pair.FamilyID {
		t.Fatalf("unexpected refresh claims: %+v", refresh)
	}
	if _, err := manager.ParseAccess(pair.RefreshToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("refresh token accepted as access token: %v", err)
	}
}

func TestManagerRejectsWrongIssuerAlgorithmSignatureAndExpiration(t *testing.T) {
	manager, _ := NewManager(testSecret, "expected-issuer", time.Hour, 24*time.Hour)
	otherIssuer, _ := NewManager(testSecret, "other-issuer", time.Hour, 24*time.Hour)
	pair, _ := otherIssuer.IssuePair(1, "alice", "")
	if _, err := manager.ParseAccess(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong issuer accepted: %v", err)
	}

	wrongSecret, _ := NewManager("abcdef0123456789abcdef0123456789", "expected-issuer", time.Hour, 24*time.Hour)
	pair, _ = wrongSecret.IssuePair(1, "alice", "")
	if _, err := manager.ParseAccess(pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong signature accepted: %v", err)
	}

	now := time.Now().UTC()
	claims := Claims{UserID: 1, Username: "alice", TokenType: TokenTypeAccess,
		RegisteredClaims: registered("expected-issuer", 1, "jti", now.Add(-2*time.Hour), now.Add(-time.Hour))}
	expired, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if _, err := manager.ParseAccess(expired); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token accepted: %v", err)
	}

	unsigned, _ := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := manager.ParseAccess(unsigned); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("none algorithm accepted: %v", err)
	}
}

func TestManagerRejectsWeakConfiguration(t *testing.T) {
	if _, err := NewManager("short", "my-im", time.Hour, time.Hour); err == nil {
		t.Fatal("expected weak secret error")
	}
}
