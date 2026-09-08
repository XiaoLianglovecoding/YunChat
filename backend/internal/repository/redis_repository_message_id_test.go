package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type failingMessageIDs struct{ err error }

func (g failingMessageIDs) Next(context.Context) (int64, error) { return 0, g.err }

func TestMessageChecksFailBeforeRedisWhenGeneratorIsUnavailable(t *testing.T) {
	for name, call := range map[string]func(*RedisRepoImpl) error{
		"private": func(repo *RedisRepoImpl) error {
			_, err := repo.ExecPrivateMsgCheck(context.Background(), 1, 2, "client")
			return err
		},
		"group": func(repo *RedisRepoImpl) error {
			_, err := repo.ExecGroupMsgCheck(context.Background(), 1, 2, "client")
			return err
		},
	} {
		t.Run(name+" missing generator", func(t *testing.T) {
			err := call(NewRedisRepo(nil))
			if err == nil || !strings.Contains(err.Error(), "not configured") {
				t.Fatalf("error = %v, want missing generator", err)
			}
		})
		t.Run(name+" generator failure", func(t *testing.T) {
			want := errors.New("allocator unavailable")
			err := call(NewRedisRepo(nil, WithMessageIDGenerator(failingMessageIDs{err: want})))
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want wrapped allocator error", err)
			}
		})
	}
}
