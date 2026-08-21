package middleware

import (
	"fmt"
	"runtime/debug"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/httpx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func Recovery(log *zap.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(ctx *gin.Context, recovered any) {
		log.Error("request panic recovered",
			zap.String("request_id", ctx.GetString(httpx.RequestIDKey)),
			zap.String("panic", fmt.Sprint(recovered)),
			zap.ByteString("stack", debug.Stack()),
		)
		httpx.Error(ctx, fmt.Errorf("panic recovered"))
	})
}
