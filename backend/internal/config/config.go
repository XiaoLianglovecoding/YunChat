package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	App      App      `yaml:"app"`
	Server   Server   `yaml:"server"`
	MySQL    MySQL    `yaml:"mysql"`
	Redis    Redis    `yaml:"redis"`
	RabbitMQ RabbitMQ `yaml:"rabbitmq"`
	JWT      JWT      `yaml:"jwt"`
	ID       ID       `yaml:"id"`
	Storage  Storage  `yaml:"storage"`
	Log      Log      `yaml:"log"`
	Auth     Auth     `yaml:"auth"`
}

type App struct {
	Name string `yaml:"name"`
	Env  string `yaml:"env"`
}

type Server struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	WebSocketPath   string        `yaml:"websocket_path"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	IdleTimeout     time.Duration `yaml:"idle_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	AllowedOrigins  []string      `yaml:"allowed_origins"`
}

type MySQL struct {
	Host                  string        `yaml:"host"`
	Port                  int           `yaml:"port"`
	Database              string        `yaml:"database"`
	User                  string        `yaml:"user"`
	Password              string        `yaml:"password"`
	MaxOpenConnections    int           `yaml:"max_open_connections"`
	MaxIdleConnections    int           `yaml:"max_idle_connections"`
	ConnectionMaxLifetime time.Duration `yaml:"connection_max_lifetime"`
}

type Redis struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	Database int    `yaml:"database"`
}

type RabbitMQ struct {
	URL      string `yaml:"url"`
	Exchange string `yaml:"exchange"`
}

type JWT struct {
	Issuer     string        `yaml:"issuer"`
	Secret     string        `yaml:"secret"`
	AccessTTL  time.Duration `yaml:"access_ttl"`
	RefreshTTL time.Duration `yaml:"refresh_ttl"`
}

type ID struct {
	WorkerID uint16 `yaml:"worker_id"`
}

type Storage struct {
	Provider       string `yaml:"provider"`
	LocalDir       string `yaml:"local_dir"`
	MaxUploadBytes int64  `yaml:"max_upload_bytes"`
}

type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type Auth struct {
	RegisterLimit  int           `yaml:"register_limit"`
	RegisterWindow time.Duration `yaml:"register_window"`
	LoginLimit     int           `yaml:"login_limit"`
	LoginWindow    time.Duration `yaml:"login_window"`
}

func Default() Config {
	return Config{
		App: App{Name: "linknest-im", Env: "local"},
		Server: Server{
			Host:            "0.0.0.0",
			Port:            18080,
			WebSocketPath:   "/ws",
			ReadTimeout:     10 * time.Second,
			WriteTimeout:    15 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 10 * time.Second,
			AllowedOrigins:  []string{"http://localhost:5173"},
		},
		MySQL: MySQL{
			Host: "127.0.0.1", Port: 3306, Database: "linknest", User: "linknest",
			MaxOpenConnections: 30, MaxIdleConnections: 10, ConnectionMaxLifetime: 30 * time.Minute,
		},
		Redis:    Redis{Addr: "127.0.0.1:6379"},
		RabbitMQ: RabbitMQ{Exchange: "linknest.events"},
		JWT:      JWT{Issuer: "linknest-im", AccessTTL: 2 * time.Hour, RefreshTTL: 7 * 24 * time.Hour},
		ID:       ID{WorkerID: 1},
		Storage:  Storage{Provider: "local", LocalDir: "./data/uploads", MaxUploadBytes: 50 << 20},
		Log:      Log{Level: "debug", Format: "console"},
		Auth:     Auth{RegisterLimit: 5, RegisterWindow: time.Hour, LoginLimit: 10, LoginWindow: time.Minute},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, fmt.Errorf("open config: %w", err)
		}
		defer file.Close()

		decoder := yaml.NewDecoder(file)
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
	}

	if err := applyEnvironment(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	var problems []string
	if strings.TrimSpace(cfg.App.Name) == "" {
		problems = append(problems, "app.name is required")
	}
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		problems = append(problems, "server.port must be between 1 and 65535")
	}
	if !strings.HasPrefix(cfg.Server.WebSocketPath, "/") {
		problems = append(problems, "server.websocket_path must start with /")
	}
	if cfg.ID.WorkerID > 1023 {
		problems = append(problems, "id.worker_id must be between 0 and 1023")
	}
	if len(cfg.JWT.Secret) < 32 {
		problems = append(problems, "jwt.secret must contain at least 32 characters")
	}
	if cfg.JWT.AccessTTL <= 0 || cfg.JWT.RefreshTTL <= 0 {
		problems = append(problems, "jwt token TTL values must be positive")
	}
	if cfg.Auth.RegisterLimit <= 0 || cfg.Auth.RegisterWindow <= 0 || cfg.Auth.LoginLimit <= 0 || cfg.Auth.LoginWindow <= 0 {
		problems = append(problems, "auth rate limit values must be positive")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

func applyEnvironment(cfg *Config) error {
	overrides := []struct {
		key string
		set func(string) error
	}{
		{"LINKNEST_APP_ENV", stringSetter(&cfg.App.Env)},
		{"LINKNEST_SERVER_HOST", stringSetter(&cfg.Server.Host)},
		{"LINKNEST_SERVER_PORT", intSetter(&cfg.Server.Port)},
		{"LINKNEST_MYSQL_HOST", stringSetter(&cfg.MySQL.Host)},
		{"LINKNEST_MYSQL_PORT", intSetter(&cfg.MySQL.Port)},
		{"LINKNEST_MYSQL_DATABASE", stringSetter(&cfg.MySQL.Database)},
		{"LINKNEST_MYSQL_USER", stringSetter(&cfg.MySQL.User)},
		{"LINKNEST_MYSQL_PASSWORD", stringSetter(&cfg.MySQL.Password)},
		{"LINKNEST_REDIS_ADDR", stringSetter(&cfg.Redis.Addr)},
		{"LINKNEST_REDIS_PASSWORD", stringSetter(&cfg.Redis.Password)},
		{"LINKNEST_RABBITMQ_URL", stringSetter(&cfg.RabbitMQ.URL)},
		{"LINKNEST_JWT_SECRET", stringSetter(&cfg.JWT.Secret)},
		{"LINKNEST_LOG_LEVEL", stringSetter(&cfg.Log.Level)},
	}

	for _, override := range overrides {
		value, ok := os.LookupEnv(override.key)
		if !ok {
			continue
		}
		if err := override.set(value); err != nil {
			return fmt.Errorf("invalid %s: %w", override.key, err)
		}
	}
	return nil
}

func stringSetter(target *string) func(string) error {
	return func(value string) error {
		*target = value
		return nil
	}
}

func intSetter(target *int) func(string) error {
	return func(value string) error {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		*target = parsed
		return nil
	}
}

func IsNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
