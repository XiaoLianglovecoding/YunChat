package infra

import (
	"context"
	"database/sql"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"my-im/internal/config"
	"my-im/internal/observability"
)

type Dependencies struct {
	DB      *sql.DB
	Redis   *goredis.Client
	Rabbit  *RabbitMQ
	Config  *config.Config
	Metrics *observability.Metrics
}

// Readiness 并行检查三项硬依赖。HTTP 进程活着不代表它具备接收业务流量的条件。
func (d *Dependencies) Readiness(ctx context.Context) map[string]error {
	type result struct {
		name string
		err  error
	}
	results := make(chan result, 3)
	go func() {
		results <- result{"mysql", CheckMySQL(ctx, d.DB, config.Milliseconds(d.Config.MySQL.ConnectTimeoutMS))}
	}()
	go func() {
		results <- result{"redis", CheckRedis(ctx, d.Redis, config.Milliseconds(d.Config.Redis.HealthTimeoutMS))}
	}()
	go func() {
		results <- result{"rabbitmq", d.Rabbit.Check(ctx, config.Milliseconds(d.Config.RabbitMQ.HealthTimeoutMS))}
	}()
	report := make(map[string]error, 3)
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	for len(report) < 3 {
		select {
		case item := <-results:
			report[item.name] = item.err
		case <-ctx.Done():
			return fillReadiness(report, ctx.Err())
		case <-deadline.C:
			return fillReadiness(report, context.DeadlineExceeded)
		}
	}
	if d.Metrics != nil {
		for queue, depth := range d.Rabbit.QueueDepths() {
			d.Metrics.SetMQBacklog(queue, depth)
		}
	}
	return report
}

func fillReadiness(report map[string]error, err error) map[string]error {
	for _, name := range []string{"mysql", "redis", "rabbitmq"} {
		if _, ok := report[name]; !ok {
			report[name] = err
		}
	}
	return report
}

// Close 严格按创建的逆序关闭：RabbitMQ -> Redis -> MySQL。
func (d *Dependencies) Close() error {
	var errs []error
	if d.Rabbit != nil {
		if err := d.Rabbit.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if d.Redis != nil {
		if err := d.Redis.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if d.DB != nil {
		if err := d.DB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
