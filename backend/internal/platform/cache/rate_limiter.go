package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var incrementWindow = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('PTTL', KEYS[1])
return {count, ttl}
`)

type RateLimiter struct{ client *redis.Client }

func NewRateLimiter(client *redis.Client) *RateLimiter { return &RateLimiter{client: client} }

func (limiter *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	result, err := incrementWindow.Run(ctx, limiter.client, []string{"ln:rate:" + key}, window.Milliseconds()).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("apply rate limit: %w", err)
	}
	if len(result) != 2 {
		return false, 0, fmt.Errorf("unexpected rate limit result")
	}
	return result[0] <= int64(limit), time.Duration(result[1]) * time.Millisecond, nil
}
