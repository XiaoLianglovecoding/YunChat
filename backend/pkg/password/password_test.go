package password

import "testing"

func TestHashAndVerify(t *testing.T) {
	hash, err := Hash("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(hash, "correct-horse-battery-staple") {
		t.Fatal("expected password to match")
	}
	if Verify(hash, "wrong-password") {
		t.Fatal("unexpected password match")
	}
}
