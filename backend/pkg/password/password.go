package password

import (
	"errors"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	MinLength = 8
	MaxLength = 72
)

func Hash(plain string) (string, error) {
	length := utf8.RuneCountInString(plain)
	if length < MinLength || len([]byte(plain)) > MaxLength {
		return "", errors.New("password must be at least 8 characters and at most 72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func Verify(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
