package observability

import (
	"fmt"
	"net/http"
	"net/http/pprof"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func NewLogger(level string) (*zap.Logger, error) {
	var parsed zapcore.Level
	if err := parsed.Set(level); err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", level, err)
	}
	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(parsed)
	cfg.Encoding = "json"
	return cfg.Build()
}

// NewPprofServer 使用独立、默认仅本机监听的端口，避免把调试接口暴露在业务路由。
func NewPprofServer(addr string) *http.Server {
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 2e9}
}
