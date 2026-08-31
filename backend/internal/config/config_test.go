package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesInfrastructureDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := []byte("app:\n  name: my-im\nmysql:\n  host: localhost\n  user: u\n  db_name: my_im\nredis:\n  addr: localhost:6379\nrabbitmq:\n  url: amqp://guest:guest@localhost/\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MySQL.MaxOpenConns != 50 || cfg.MySQL.QueryTimeoutMS != 3000 {
		t.Fatalf("unexpected MySQL defaults: %+v", cfg.MySQL)
	}
	if cfg.Observability.MetricsPath != "/metrics" {
		t.Fatalf("metrics path = %q", cfg.Observability.MetricsPath)
	}
}

func TestValidateRejectsOversizedIdlePool(t *testing.T) {
	cfg := Config{App: AppConfig{Name: "my-im"}, MySQL: MySQLConfig{Host: "h", User: "u", DBName: "d", MaxOpenConns: 1, MaxIdleConns: 2}, Redis: RedisConfig{Addr: "r"}, RabbitMQ: RabbitMQConfig{URL: "amqp://x"}, Server: ServerConfig{WSPath: "/ws"}, Observability: ObservabilityConfig{MetricsPath: "/metrics"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
