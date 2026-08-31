package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/migrate"
)

func main() {
	configPath := flag.String("c", "configs/config.example.yaml", "配置文件路径")
	flag.Parse()
	command := "status"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}
	if command != "up" && command != "status" {
		log.Fatalf("未知命令 %q；只支持 up/status", command)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	db, err := infra.OpenMySQL(context.Background(), cfg.MySQL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	runner := migrate.New(db, cfg.App.MigrationDir)
	if command == "up" {
		if err := runner.Up(context.Background()); err != nil {
			log.Fatal(err)
		}
		fmt.Println("数据库迁移完成")
	}
	states, err := runner.Status(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	if len(states) == 0 {
		fmt.Println("尚未应用任何迁移")
		return
	}
	for _, state := range states {
		status := "applied"
		if state.Dirty {
			status = "DIRTY"
		}
		fmt.Fprintf(os.Stdout, "%03d  %-8s  %s\n", state.Version, status, state.Name)
	}
}
