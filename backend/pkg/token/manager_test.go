package token

import (
	"testing"
	"time"
)

func TestSignAndParseAccessToken(t *testing.T) {
	manager, err := NewManager("linknest-test", "01234567890123456789012345678901", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	raw, _, err := manager.SignAccess(42, 7, 9)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.ParseAccess(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != 42 || claims.DeviceID != 7 || claims.SessionID != 9 {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}
