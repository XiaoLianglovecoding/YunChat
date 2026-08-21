package validate

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{3,31}$`)

func Username(value string) error {
	if !usernamePattern.MatchString(value) {
		return errors.New("username must start with a letter and contain 4-32 letters, digits, or underscores")
	}
	return nil
}

func Nickname(value string) error {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	if length < 1 || length > 30 {
		return errors.New("nickname must contain 1-30 characters")
	}
	return nil
}

func PageLimit(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}
