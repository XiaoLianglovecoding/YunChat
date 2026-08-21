package logger

import (
	"fmt"
	"strings"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(cfg config.Log, environment string) (*zap.Logger, error) {
	var zapConfig zap.Config
	if environment == "production" {
		zapConfig = zap.NewProductionConfig()
	} else {
		zapConfig = zap.NewDevelopmentConfig()
	}

	if strings.EqualFold(cfg.Format, "json") {
		zapConfig.Encoding = "json"
	} else {
		zapConfig.Encoding = "console"
	}

	level := zapcore.InfoLevel
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}
	zapConfig.Level = zap.NewAtomicLevelAt(level)
	zapConfig.EncoderConfig.TimeKey = "timestamp"
	zapConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	return zapConfig.Build()
}
