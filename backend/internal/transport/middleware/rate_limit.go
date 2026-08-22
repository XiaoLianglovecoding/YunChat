package middleware

import (
	"context"
	"strconv"
	"time"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/httpx"
	"github.com/gin-gonic/gin"
)

type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)
}

func RateLimit(limiter RateLimiter, scope string, limit int, window time.Duration) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if limiter == nil {
			ctx.Next()
			return
		}
		allowed, retryAfter, err := limiter.Allow(ctx, scope+":"+ctx.ClientIP(), limit, window)
		if err != nil {
			httpx.ServiceUnavailable(ctx)
			ctx.Abort()
			return
		}
		if !allowed {
			seconds := max(1, int(retryAfter.Round(time.Second).Seconds()))
			ctx.Header("Retry-After", strconv.Itoa(seconds))
			httpx.TooManyRequests(ctx)
			ctx.Abort()
			return
		}
		ctx.Next()
	}
}
