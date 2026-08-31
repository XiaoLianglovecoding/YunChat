package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"my-im/internal/api"
	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/migrate"
	"my-im/internal/observability"
	redisscripts "my-im/internal/redis"
	"my-im/internal/repository"
)

func main() {
	configPath := flag.String("c", "configs/config.example.yaml", "配置文件路径")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Fatalf("my-im 启动失败: %v", err)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logger, err := observability.NewLogger(cfg.Observability.LogLevel)
	if err != nil {
		return err
	}
	defer logger.Sync() // Windows 上 stderr Sync 可能返回无害错误，不覆盖主流程结果。
	metrics := observability.NewMetrics()

	db, err := infra.OpenMySQL(context.Background(), cfg.MySQL)
	if err != nil {
		return err
	}
	if cfg.App.AutoMigrate {
		if err := migrate.New(db, cfg.App.MigrationDir).Up(context.Background()); err != nil {
			_ = db.Close()
			return fmt.Errorf("database migration failed; HTTP traffic was not started: %w", err)
		}
	}
	redisClient, err := infra.OpenRedis(context.Background(), cfg.Redis)
	if err != nil {
		_ = db.Close()
		return err
	}
	rabbit, err := infra.OpenRabbitMQ(context.Background(), cfg.RabbitMQ, metrics.ObserveMQ)
	if err != nil {
		_ = redisClient.Close()
		_ = db.Close()
		return err
	}
	deps := &infra.Dependencies{DB: db, Redis: redisClient, Rabbit: rabbit, Config: cfg, Metrics: metrics}
	defer func() {
		if err := deps.Close(); err != nil {
			logger.Error("close_dependencies", zap.Error(err))
		}
	}()

	// 完成端口装配；后续 Service 任务直接依赖这些接口，不再接触连接对象。
	mysqlRepo := repository.NewMySQLRepository(db, config.Milliseconds(cfg.MySQL.QueryTimeoutMS), metrics.ObserveDB)
	redisRepo := repository.NewRedisRepo(redisClient)
	publisher := repository.NewRabbitPublisher(rabbit)
	_, _, _ = mysqlRepo, redisRepo, publisher
	logger.Info("lua_scripts_loaded", zap.Any("sha", redisscripts.LuaScriptHashes()))

	if err := os.MkdirAll(cfg.Server.UploadDir, 0o755); err != nil {
		return fmt.Errorf("create upload directory: %w", err)
	}
	router := api.NewRouter(api.RouterOptions{
		ServiceName: cfg.App.Name, WSPath: cfg.Server.WSPath, UploadDir: cfg.Server.UploadDir,
		FrontendDir: cfg.Server.FrontendDir, AllowedOrigins: cfg.Server.AllowedOrigins,
		Readiness: deps.Readiness, Logger: logger, Metrics: metrics, MetricsPath: cfg.Observability.MetricsPath,
	})
	server := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.Server.Port), Handler: router,
		ReadHeaderTimeout: config.Milliseconds(cfg.Server.ReadHeaderTimeoutMS),
	}
	pprofServer := observability.NewPprofServer(cfg.Observability.PprofAddr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serverErr := make(chan error, 2)
	go func() {
		logger.Info("http_server_started", zap.String("service", cfg.App.Name), zap.String("address", server.Addr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()
	if pprofServer != nil {
		go func() {
			logger.Info("pprof_server_started", zap.String("address", pprofServer.Addr))
			if err := pprofServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverErr <- fmt.Errorf("pprof: %w", err)
			}
		}()
	}

	select {
	case <-ctx.Done():
		logger.Info("shutdown_signal_received")
	case err := <-serverErr:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Milliseconds(cfg.Server.ShutdownTimeoutMS))
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	if pprofServer != nil {
		if err := pprofServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("shutdown_pprof", zap.Error(err))
		}
	}
	return nil
}
