package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/repository"
	"my-im/internal/service"
)

func main() {
	configPath := flag.String("c", "configs/config.example.yaml", "配置文件路径")
	action := flag.String("action", "audit", "操作：rebuild 或 audit")
	scope := flag.String("scope", "all", "范围：friends、blacklists、groups 或 all")
	timeout := flag.Duration("timeout", 10*time.Minute, "整个操作的超时时间")
	flag.Parse()

	if *action != "rebuild" && *action != "audit" {
		log.Fatalf("未知 action %q；只支持 rebuild/audit", *action)
	}
	cacheScope := service.CacheScope(*scope)
	if !cacheScope.Valid() {
		log.Fatalf("未知 scope %q；只支持 friends/blacklists/groups/all", *scope)
	}

	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(baseCtx, *timeout)
	defer cancel()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	mysqlDB, err := infra.OpenMySQL(ctx, cfg.MySQL)
	if err != nil {
		log.Fatal(err)
	}
	defer mysqlDB.Close()
	redisClient, err := infra.OpenRedis(ctx, cfg.Redis)
	if err != nil {
		log.Fatal(err)
	}
	defer redisClient.Close()

	mysqlRepo := repository.NewMySQLRepository(mysqlDB, config.Milliseconds(cfg.MySQL.QueryTimeoutMS), nil)
	redisRepo := repository.NewRedisRepo(redisClient)
	cacheTruth := service.NewCacheTruthService(mysqlRepo, redisRepo, service.CacheTruthOptions{})

	var result any
	if *action == "rebuild" {
		result, err = cacheTruth.Rebuild(ctx, cacheScope)
	} else {
		result, err = cacheTruth.Audit(ctx, cacheScope)
	}
	if err != nil {
		log.Fatal(err)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(encoded))

	if report, ok := result.(service.CacheAuditReport); ok && report.Mismatches > 0 {
		os.Exit(2)
	}
}
