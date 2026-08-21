package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/config"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/platform/logger"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("worker scaffold started")
	// TODO(linknest): wire outbox polling, RabbitMQ publishing, and idempotent consumers.
	<-ctx.Done()
	log.Info("worker stopped", zap.Error(ctx.Err()))
}
