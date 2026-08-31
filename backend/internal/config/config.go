package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 MyIM 的唯一运行时配置入口。密码只从环境变量展开，代码不会打印配置全文。
type Config struct {
	App           AppConfig           `yaml:"app"`
	Server        ServerConfig        `yaml:"server"`
	MySQL         MySQLConfig         `yaml:"mysql"`
	Redis         RedisConfig         `yaml:"redis"`
	RabbitMQ      RabbitMQConfig      `yaml:"rabbitmq"`
	Observability ObservabilityConfig `yaml:"observability"`
	JWT           JWTConfig           `yaml:"jwt"`
	File          FileConfig          `yaml:"file"`
	Moment        MomentConfig        `yaml:"moment"`
}

type AppConfig struct {
	Name         string `yaml:"name"`
	Environment  string `yaml:"environment"`
	AutoMigrate  bool   `yaml:"auto_migrate"`
	MigrationDir string `yaml:"migration_dir"`
}

type ServerConfig struct {
	Port                int      `yaml:"port"`
	WSPath              string   `yaml:"ws_path"`
	UploadDir           string   `yaml:"upload_dir"`
	FrontendDir         string   `yaml:"frontend_dir"`
	AllowedOrigins      []string `yaml:"allowed_origins"`
	ReadHeaderTimeoutMS int      `yaml:"read_header_timeout_ms"`
	ShutdownTimeoutMS   int      `yaml:"shutdown_timeout_ms"`
}

type MySQLConfig struct {
	Host              string `yaml:"host"`
	Port              int    `yaml:"port"`
	User              string `yaml:"user"`
	Password          string `yaml:"password"`
	DBName            string `yaml:"db_name"`
	ConnectTimeoutMS  int    `yaml:"connect_timeout_ms"`
	QueryTimeoutMS    int    `yaml:"query_timeout_ms"`
	MaxOpenConns      int    `yaml:"max_open_conns"`
	MaxIdleConns      int    `yaml:"max_idle_conns"`
	ConnMaxLifetimeMS int    `yaml:"conn_max_lifetime_ms"`
	ConnMaxIdleTimeMS int    `yaml:"conn_max_idle_time_ms"`
}

type RedisConfig struct {
	Addr            string `yaml:"addr"`
	Password        string `yaml:"password"`
	DB              int    `yaml:"db"`
	DialTimeoutMS   int    `yaml:"dial_timeout_ms"`
	ReadTimeoutMS   int    `yaml:"read_timeout_ms"`
	WriteTimeoutMS  int    `yaml:"write_timeout_ms"`
	PoolSize        int    `yaml:"pool_size"`
	MinIdleConns    int    `yaml:"min_idle_conns"`
	HealthTimeoutMS int    `yaml:"health_timeout_ms"`
}

type RabbitMQConfig struct {
	URL              string `yaml:"url"`
	ConnectTimeoutMS int    `yaml:"connect_timeout_ms"`
	PublishTimeoutMS int    `yaml:"publish_timeout_ms"`
	RetryCount       int    `yaml:"retry_count"`
	RetryBackoffMS   int    `yaml:"retry_backoff_ms"`
	HealthTimeoutMS  int    `yaml:"health_timeout_ms"`
}

type ObservabilityConfig struct {
	LogLevel    string `yaml:"log_level"`
	MetricsPath string `yaml:"metrics_path"`
	PprofAddr   string `yaml:"pprof_addr"`
}

type JWTConfig struct {
	Secret         string `yaml:"secret"`
	AccessExpHours int    `yaml:"access_exp_hours"`
	RefreshExpDays int    `yaml:"refresh_exp_days"`
}

type FileConfig struct {
	MaxSizeMB   int      `yaml:"max_size_mb"`
	AllowedExts []string `yaml:"allowed_exts"`
}

type MomentConfig struct {
	BigUserFriendThreshold int `yaml:"big_user_friend_threshold"`
	TimelineMaxLen         int `yaml:"timeline_max_len"`
	LikePersistBatchSize   int `yaml:"like_persist_batch_size"`
	LikePersistFlushMS     int `yaml:"like_persist_flush_ms"`
	LikeCacheTTLHours      int `yaml:"like_cache_ttl_hours"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(data))), &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}
	return &cfg, nil
}

func (cfg *Config) applyDefaults() {
	if cfg.App.Name == "" {
		cfg.App.Name = "my-im"
	}
	if cfg.App.Environment == "" {
		cfg.App.Environment = "development"
	}
	if cfg.App.MigrationDir == "" {
		cfg.App.MigrationDir = "scripts/migrations"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 18080
	}
	if cfg.Server.WSPath == "" {
		cfg.Server.WSPath = "/ws"
	}
	if cfg.Server.UploadDir == "" {
		cfg.Server.UploadDir = "./uploads"
	}
	if cfg.Server.ReadHeaderTimeoutMS <= 0 {
		cfg.Server.ReadHeaderTimeoutMS = 5000
	}
	if cfg.Server.ShutdownTimeoutMS <= 0 {
		cfg.Server.ShutdownTimeoutMS = 10000
	}
	if cfg.MySQL.Port == 0 {
		cfg.MySQL.Port = 3306
	}
	if cfg.MySQL.ConnectTimeoutMS <= 0 {
		cfg.MySQL.ConnectTimeoutMS = 3000
	}
	if cfg.MySQL.QueryTimeoutMS <= 0 {
		cfg.MySQL.QueryTimeoutMS = 3000
	}
	if cfg.MySQL.MaxOpenConns <= 0 {
		cfg.MySQL.MaxOpenConns = 50
	}
	if cfg.MySQL.MaxIdleConns <= 0 {
		cfg.MySQL.MaxIdleConns = 10
	}
	if cfg.MySQL.ConnMaxLifetimeMS <= 0 {
		cfg.MySQL.ConnMaxLifetimeMS = 300000
	}
	if cfg.MySQL.ConnMaxIdleTimeMS <= 0 {
		cfg.MySQL.ConnMaxIdleTimeMS = 60000
	}
	if cfg.Redis.DialTimeoutMS <= 0 {
		cfg.Redis.DialTimeoutMS = 3000
	}
	if cfg.Redis.ReadTimeoutMS <= 0 {
		cfg.Redis.ReadTimeoutMS = 2000
	}
	if cfg.Redis.WriteTimeoutMS <= 0 {
		cfg.Redis.WriteTimeoutMS = 2000
	}
	if cfg.Redis.PoolSize <= 0 {
		cfg.Redis.PoolSize = 20
	}
	if cfg.Redis.MinIdleConns < 0 {
		cfg.Redis.MinIdleConns = 0
	}
	if cfg.Redis.HealthTimeoutMS <= 0 {
		cfg.Redis.HealthTimeoutMS = 1000
	}
	if cfg.RabbitMQ.ConnectTimeoutMS <= 0 {
		cfg.RabbitMQ.ConnectTimeoutMS = 5000
	}
	if cfg.RabbitMQ.PublishTimeoutMS <= 0 {
		cfg.RabbitMQ.PublishTimeoutMS = 3000
	}
	if cfg.RabbitMQ.RetryCount <= 0 {
		cfg.RabbitMQ.RetryCount = 3
	}
	if cfg.RabbitMQ.RetryBackoffMS <= 0 {
		cfg.RabbitMQ.RetryBackoffMS = 200
	}
	if cfg.RabbitMQ.HealthTimeoutMS <= 0 {
		cfg.RabbitMQ.HealthTimeoutMS = 1000
	}
	if cfg.Observability.LogLevel == "" {
		cfg.Observability.LogLevel = "info"
	}
	if cfg.Observability.MetricsPath == "" {
		cfg.Observability.MetricsPath = "/metrics"
	}
	if cfg.Moment.BigUserFriendThreshold <= 0 {
		cfg.Moment.BigUserFriendThreshold = 500
	}
	if cfg.Moment.TimelineMaxLen <= 0 {
		cfg.Moment.TimelineMaxLen = 1000
	}
	if cfg.Moment.LikePersistBatchSize <= 0 {
		cfg.Moment.LikePersistBatchSize = 200
	}
	if cfg.Moment.LikePersistFlushMS <= 0 {
		cfg.Moment.LikePersistFlushMS = 500
	}
	if cfg.Moment.LikeCacheTTLHours <= 0 {
		cfg.Moment.LikeCacheTTLHours = 168
	}
}

func (cfg Config) Validate() error {
	missing := make([]string, 0)
	for name, value := range map[string]string{
		"app.name": cfg.App.Name, "mysql.host": cfg.MySQL.Host, "mysql.user": cfg.MySQL.User,
		"mysql.db_name": cfg.MySQL.DBName, "redis.addr": cfg.Redis.Addr, "rabbitmq.url": cfg.RabbitMQ.URL,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("required fields are empty: %s", strings.Join(missing, ", "))
	}
	if cfg.MySQL.MaxIdleConns > cfg.MySQL.MaxOpenConns {
		return fmt.Errorf("mysql.max_idle_conns must not exceed max_open_conns")
	}
	if !strings.HasPrefix(cfg.Server.WSPath, "/") || !strings.HasPrefix(cfg.Observability.MetricsPath, "/") {
		return fmt.Errorf("server.ws_path and observability.metrics_path must start with /")
	}
	return nil
}

func Milliseconds(v int) time.Duration { return time.Duration(v) * time.Millisecond }
