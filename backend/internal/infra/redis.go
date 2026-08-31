package infra

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"my-im/internal/config"
	redisscripts "my-im/internal/redis"
)

func OpenRedis(ctx context.Context, cfg config.RedisConfig) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB,
		DialTimeout: config.Milliseconds(cfg.DialTimeoutMS), ReadTimeout: config.Milliseconds(cfg.ReadTimeoutMS),
		WriteTimeout: config.Milliseconds(cfg.WriteTimeoutMS), PoolSize: cfg.PoolSize, MinIdleConns: cfg.MinIdleConns,
	})
	checkCtx, cancel := context.WithTimeout(ctx, config.Milliseconds(cfg.HealthTimeoutMS))
	defer cancel()
	if err := client.Ping(checkCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	if err := redisscripts.LoadLuaScripts(client, checkCtx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func CheckRedis(ctx context.Context, client *goredis.Client, timeout time.Duration) error {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := client.Ping(checkCtx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}
