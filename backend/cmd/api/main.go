package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/application"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/config"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/platform/logger"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/realtime"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/id"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/token"
	"go.uber.org/zap"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to the YAML configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		panic(err)
	}
	log, err := logger.New(cfg.Log, cfg.App.Env)
	if err != nil {
		panic(err)
	}
	defer log.Sync() //nolint:errcheck

	idGenerator, err := id.New(cfg.ID.WorkerID)
	if err != nil {
		log.Fatal("initialize id generator", zap.Error(err))
	}
	_ = idGenerator // TODO(linknest): inject into concrete application services.

	tokens, err := token.NewManager(cfg.JWT.Issuer, cfg.JWT.Secret, cfg.JWT.AccessTTL)
	if err != nil {
		log.Fatal("initialize token manager", zap.Error(err))
	}

	hub := realtime.NewHub()
	services := application.NewTODO()
	router := transport.NewRouter(cfg, log, tokens, services, hub)
	server := &http.Server{
		Addr:         net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	shutdownSignals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("api listening", zap.String("address", server.Addr), zap.String("environment", cfg.App.Env))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("api server stopped unexpectedly", zap.Error(err))
		}
	}()

	<-shutdownSignals.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		log.Error("graceful shutdown failed", zap.Error(err))
	}
	log.Info("api stopped")
}
