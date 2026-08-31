package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 保留原 GoIM 的运行时边界。当前骨架只使用 Server 配置；
// MySQL、Redis、RabbitMQ、JWT 等配置将在对应 TODO 完成后接入装配层。
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	MySQL    MySQLConfig    `yaml:"mysql"`
	Redis    RedisConfig    `yaml:"redis"`
	RabbitMQ RabbitMQConfig `yaml:"rabbitmq"`
	JWT      JWTConfig      `yaml:"jwt"`
	File     FileConfig     `yaml:"file"`
	Moment   MomentConfig   `yaml:"moment"`
}

type ServerConfig struct {
	Port           int      `yaml:"port"`
	WSPath         string   `yaml:"ws_path"`
	UploadDir      string   `yaml:"upload_dir"`
	FrontendDir    string   `yaml:"frontend_dir"`
	AllowedOrigins []string `yaml:"allowed_origins"`
}

type MySQLConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"db_name"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type RabbitMQConfig struct {
	URL string `yaml:"url"`
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
	return &cfg, nil
}

func (cfg *Config) applyDefaults() {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 18080
	}
	if cfg.Server.WSPath == "" {
		cfg.Server.WSPath = "/ws"
	}
	if cfg.Server.UploadDir == "" {
		cfg.Server.UploadDir = "./uploads"
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
