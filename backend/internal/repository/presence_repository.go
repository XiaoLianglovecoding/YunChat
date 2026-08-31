package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

var (
	claimPresenceScript = goredis.NewScript(`
local wasOffline = redis.call('EXISTS', KEYS[2]) == 0
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('SET', KEYS[2], ARGV[1], 'PX', ARGV[2])
if wasOffline then return 1 else return 0 end
`)
	renewPresenceScript = goredis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('PEXPIRE', KEYS[1], ARGV[2])
redis.call('SET', KEYS[2], ARGV[1], 'PX', ARGV[2])
return 1
`)
	releasePresenceScript = goredis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[1], KEYS[2])
return 1
`)
)

func presenceKeys(userID int64) (string, string) {
	return fmt.Sprintf("conn:%d", userID), fmt.Sprintf("online:%d", userID)
}

// ClaimPresence makes connectionID the sole owner of a user's online lease.
// wasOffline is false for a same-user connection replacement, preventing a
// duplicate online notification.
func (r *RedisRepoImpl) ClaimPresence(ctx context.Context, userID int64, connectionID string, ttl time.Duration) (wasOffline bool, err error) {
	if userID <= 0 || connectionID == "" || ttl <= 0 {
		return false, errors.New("invalid presence lease")
	}
	connKey, onlineKey := presenceKeys(userID)
	result, err := claimPresenceScript.Run(ctx, r.rdb, []string{connKey, onlineKey}, connectionID, ttl.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("claim presence for user %d: %w", userID, err)
	}
	return result == 1, nil
}

// RenewPresence refreshes only the lease owned by connectionID. An old
// connection can therefore never extend a newer login's lease.
func (r *RedisRepoImpl) RenewPresence(ctx context.Context, userID int64, connectionID string, ttl time.Duration) (bool, error) {
	connKey, onlineKey := presenceKeys(userID)
	result, err := renewPresenceScript.Run(ctx, r.rdb, []string{connKey, onlineKey}, connectionID, ttl.Milliseconds()).Int64()
	if err != nil {
		return false, fmt.Errorf("renew presence for user %d: %w", userID, err)
	}
	return result == 1, nil
}

// ReleasePresence uses compare-and-delete. This closes the race where an old
// socket disconnects after a new socket has already claimed the same user.
func (r *RedisRepoImpl) ReleasePresence(ctx context.Context, userID int64, connectionID string) (bool, error) {
	connKey, onlineKey := presenceKeys(userID)
	result, err := releasePresenceScript.Run(ctx, r.rdb, []string{connKey, onlineKey}, connectionID).Int64()
	if err != nil {
		return false, fmt.Errorf("release presence for user %d: %w", userID, err)
	}
	return result == 1, nil
}
