package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/httpx"
	"github.com/gin-gonic/gin"
)

const requestIDHeader = "X-Request-ID"

func RequestID() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		requestID := ctx.GetHeader(requestIDHeader)
		if requestID == "" || len(requestID) > 64 {
			requestID = newRequestID()
		}
		ctx.Set(httpx.RequestIDKey, requestID)
		ctx.Header(requestIDHeader, requestID)
		ctx.Next()
	}
}

func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err == nil {
		return hex.EncodeToString(buffer)
	}
	return strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
}
