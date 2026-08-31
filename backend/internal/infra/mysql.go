package infra

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"my-im/internal/config"
)

// OpenMySQL 创建连接池并在返回前完成一次真实 Ping。sql.DB 本身是连接池，不是一条连接。
func OpenMySQL(ctx context.Context, cfg config.MySQLConfig) (*sql.DB, error) {
	driverCfg := drivermysql.NewConfig()
	driverCfg.Net = "tcp"
	driverCfg.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	driverCfg.User = cfg.User
	driverCfg.Passwd = cfg.Password
	driverCfg.DBName = cfg.DBName
	driverCfg.ParseTime = true
	driverCfg.Loc = time.UTC
	driverCfg.Collation = "utf8mb4_unicode_ci"
	driverCfg.MultiStatements = true // 一个版本化迁移文件允许包含多条 DDL。
	driverCfg.Timeout = config.Milliseconds(cfg.ConnectTimeoutMS)
	driverCfg.ReadTimeout = config.Milliseconds(cfg.QueryTimeoutMS)
	driverCfg.WriteTimeout = config.Milliseconds(cfg.QueryTimeoutMS)

	db, err := sql.Open("mysql", driverCfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(config.Milliseconds(cfg.ConnMaxLifetimeMS))
	db.SetConnMaxIdleTime(config.Milliseconds(cfg.ConnMaxIdleTimeMS))

	pingCtx, cancel := context.WithTimeout(ctx, config.Milliseconds(cfg.ConnectTimeoutMS))
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return db, nil
}

func CheckMySQL(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := db.PingContext(checkCtx); err != nil {
		return fmt.Errorf("mysql ping: %w", err)
	}
	return nil
}
