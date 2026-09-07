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
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"my-im/internal/api"
	authtoken "my-im/internal/auth"
	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/migrate"
	"my-im/internal/observability"
	redisscripts "my-im/internal/redis"
	"my-im/internal/repository"
	"my-im/internal/service"
	wsserver "my-im/internal/ws"
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
	_ = publisher
	tokenManager, err := authtoken.NewManager(cfg.JWT.Secret, cfg.JWT.Issuer,
		time.Duration(cfg.JWT.AccessExpHours)*time.Hour, time.Duration(cfg.JWT.RefreshExpDays)*24*time.Hour)
	if err != nil {
		return fmt.Errorf("initialize JWT manager: %w", err)
	}
	authService, err := service.NewAuthService(mysqlRepo, redisRepo, tokenManager, logger)
	if err != nil {
		return fmt.Errorf("initialize auth service: %w", err)
	}
	avatarService, err := service.NewAvatarService(mysqlRepo, cfg.Server.UploadDir, cfg.File.MaxSizeMB, cfg.File.AllowedExts)
	if err != nil {
		return fmt.Errorf("initialize avatar service: %w", err)
	}
	cacheTruth := service.NewCacheTruthService(mysqlRepo, redisRepo, service.CacheTruthOptions{})
	websocketHub, err := wsserver.NewHub(tokenManager, redisRepo, mysqlRepo, wsserver.HubOptions{
		AllowedOrigins: cfg.Server.AllowedOrigins,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("initialize WebSocket hub: %w", err)
	}
	friendService, err := service.NewFriendService(mysqlRepo,
		service.WithFriendCache(cacheTruth),
		service.WithFriendEventNotifier(websocketHub),
		service.WithPresenceReader(redisRepo),
	)
	if err != nil {
		return fmt.Errorf("initialize friend service: %w", err)
	}
	groupService, err := service.NewGroupService(mysqlRepo,
		service.WithGroupCache(cacheTruth),
		service.WithGroupEventNotifier(websocketHub),
	)
	if err != nil {
		return fmt.Errorf("initialize group service: %w", err)
	}
	logger.Info("lua_scripts_loaded", zap.Any("sha", redisscripts.LuaScriptHashes()))

	if err := os.MkdirAll(cfg.Server.UploadDir, 0o755); err != nil {
		return fmt.Errorf("create upload directory: %w", err)
	}
	router := api.NewRouter(api.RouterOptions{
		ServiceName: cfg.App.Name, WSPath: cfg.Server.WSPath, UploadDir: cfg.Server.UploadDir,
		FrontendDir: cfg.Server.FrontendDir, AllowedOrigins: cfg.Server.AllowedOrigins,
		Readiness: deps.Readiness, Logger: logger, Metrics: metrics, MetricsPath: cfg.Observability.MetricsPath,
		Auth: authService, TokenVerifier: tokenManager, Profile: avatarService, Upload: avatarService,
		Friend: friendService, Group: groupService, WebSocket: websocketHub.Handler, FileMaxSizeMB: cfg.File.MaxSizeMB,
	})
	server := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.Server.Port), Handler: router,
		ReadHeaderTimeout: config.Milliseconds(cfg.Server.ReadHeaderTimeoutMS),
	}
	pprofServer := observability.NewPprofServer(cfg.Observability.PprofAddr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	workerHost, _ := os.Hostname()
	workerID := fmt.Sprintf("%s-%d", workerHost, os.Getpid())
	reconciler := service.NewCacheReconciler(mysqlRepo, cacheTruth, workerID, service.CacheReconcilerOptions{
		OnError: func(err error) { logger.Warn("cache_reconcile_batch_failed", zap.Error(err)) },
	})
	var background sync.WaitGroup
	background.Add(2)
	go func() {
		defer background.Done()
		report, err := cacheTruth.Warm(ctx, service.CacheScopeAll)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("relationship_cache_warm_failed", zap.Error(err))
			}
		} else {
			logger.Info("relationship_cache_warm_completed",
				zap.Int("users", report.Users), zap.Int("groups", report.Groups), zap.Int("resources", report.Resources))
		}
	}()
	go func() {
		defer background.Done()
		// Per-resource Redis locks plus MySQL owner-row locks make warm-up and
		// durable repair safe to run together. Starting immediately avoids making
		// a pending security-sensitive blacklist repair wait for a full scan.
		if err := reconciler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("cache_reconciler_stopped", zap.Error(err))
		}
	}()
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

	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("shutdown_signal_received")
	case err := <-serverErr:
		runErr = err
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Milliseconds(cfg.Server.ShutdownTimeoutMS))
	defer cancel()
	var shutdownErrors []error
	if err := server.Shutdown(shutdownCtx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("shutdown HTTP server: %w", err))
	}
	// Stop accepting/upgrading HTTP requests before taking the WebSocket hub
	// snapshot, then close the hijacked connections that net/http does not own.
	websocketHub.Close()
	if pprofServer != nil {
		if err := pprofServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("shutdown_pprof", zap.Error(err))
			shutdownErrors = append(shutdownErrors, fmt.Errorf("shutdown pprof server: %w", err))
		}
	}
	backgroundDone := make(chan struct{})
	go func() {
		background.Wait()
		close(backgroundDone)
	}()
	select {
	case <-backgroundDone:
	case <-shutdownCtx.Done():
		logger.Warn("background_shutdown_timeout")
		shutdownErrors = append(shutdownErrors, fmt.Errorf("shutdown background workers: %w", shutdownCtx.Err()))
	}
	return errors.Join(runErr, errors.Join(shutdownErrors...))
}
