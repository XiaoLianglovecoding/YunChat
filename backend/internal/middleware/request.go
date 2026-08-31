package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"my-im/internal/observability"
)

const RequestIDKey = "request_id"

func RequestLog(logger *zap.Logger, metrics *observability.Metrics) gin.HandlerFunc {
	if logger == nil {
		logger = zap.NewNop()
	}
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 {
			requestID = newRequestID()
		}
		c.Set(RequestIDKey, requestID)
		c.Header("X-Request-ID", requestID)
		started := time.Now()
		c.Next()
		elapsed := time.Since(started)
		if metrics != nil {
			metrics.ObserveHTTP(c.Writer.Status())
		}
		logger.Info("http_request", zap.String("request_id", requestID), zap.String("method", c.Request.Method),
			zap.String("path", c.FullPath()), zap.Int("status", c.Writer.Status()), zap.Duration("latency", elapsed),
			zap.String("client_ip", c.ClientIP()))
	}
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(raw[:])
}
