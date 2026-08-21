package middleware

import (
	"net/http"
	"strings"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/httpx"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/token"
	"github.com/gin-gonic/gin"
)

const PrincipalKey = "principal"

type Principal struct {
	UserID    domain.UserID
	DeviceID  domain.DeviceID
	SessionID uint64
}

func RequireAuth(tokens *token.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		scheme, raw, ok := strings.Cut(ctx.GetHeader("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" {
			ctx.Abort()
			httpx.Error(ctx, domain.ErrUnauthorized)
			return
		}
		claims, err := tokens.ParseAccess(raw)
		if err != nil {
			ctx.Abort()
			ctx.Header("WWW-Authenticate", `Bearer realm="linknest"`)
			ctx.Status(http.StatusUnauthorized)
			httpx.Error(ctx, domain.ErrUnauthorized)
			return
		}
		ctx.Set(PrincipalKey, Principal{
			UserID: domain.UserID(claims.UserID), DeviceID: domain.DeviceID(claims.DeviceID), SessionID: claims.SessionID,
		})
		ctx.Next()
	}
}

func CurrentPrincipal(ctx *gin.Context) (Principal, bool) {
	value, exists := ctx.Get(PrincipalKey)
	principal, ok := value.(Principal)
	return principal, exists && ok
}
