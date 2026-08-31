package infra

import (
	"context"
	"os"
	"testing"
	"time"

	"my-im/internal/config"
	"my-im/internal/consumer"
	redisscripts "my-im/internal/redis"
)

// 本测试默认跳过，避免普通 go test 强制要求 Docker；本地/CI 显式设置 MYIM_INTEGRATION=1。
func TestDockerInfrastructure(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker dependencies running")
	}
	ctx := context.Background()
	mysqlCfg := config.MySQLConfig{Host: "127.0.0.1", Port: 13306, User: "my_im", Password: "my_im123", DBName: "my_im", ConnectTimeoutMS: 3000, QueryTimeoutMS: 3000, MaxOpenConns: 7, MaxIdleConns: 3, ConnMaxLifetimeMS: 300000, ConnMaxIdleTimeMS: 60000}
	db, err := OpenMySQL(ctx, mysqlCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("max open connections = %d", got)
	}
	if err := CheckMySQL(ctx, db, time.Second); err != nil {
		t.Fatal(err)
	}

	redisCfg := config.RedisConfig{Addr: "127.0.0.1:16379", DialTimeoutMS: 3000, ReadTimeoutMS: 2000, WriteTimeoutMS: 2000, PoolSize: 5, MinIdleConns: 1, HealthTimeoutMS: 1000}
	redisClient, err := OpenRedis(ctx, redisCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer redisClient.Close()
	if err := CheckRedis(ctx, redisClient, time.Second); err != nil {
		t.Fatal(err)
	}
	hashes := redisscripts.LuaScriptHashes()
	values := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		values = append(values, hash)
	}
	exists, err := redisClient.ScriptExists(ctx, values...).Result()
	if err != nil {
		t.Fatal(err)
	}
	for i, loaded := range exists {
		if !loaded {
			t.Fatalf("Lua hash %s was not loaded", values[i])
		}
	}

	rabbitCfg := config.RabbitMQConfig{URL: "amqp://my_im_mq:my_im_mq123@127.0.0.1:5673/", ConnectTimeoutMS: 5000, PublishTimeoutMS: 2000, RetryCount: 2, RetryBackoffMS: 50, HealthTimeoutMS: 1000}
	rabbit, err := OpenRabbitMQ(ctx, rabbitCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rabbit.Close()
	if err := rabbit.Check(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := rabbit.Publish(ctx, consumer.PrivateMessageQueue, []byte(`{"integration_test":true}`), "foundation-integration-test"); err != nil {
		t.Fatalf("confirmed publish: %v", err)
	}
	if err := rabbit.Publish(ctx, "queue_that_does_not_exist", []byte(`{}`), "mandatory-return-test"); err == nil {
		t.Fatal("expected mandatory return for missing queue")
	}
}
