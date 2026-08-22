package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

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

type SessionValidator interface {
	IsSessionActive(ctx context.Context, userID domain.UserID, deviceID domain.DeviceID, tokenID uint64, now time.Time) (bool, error)
}

func RequireAuth(tokens *token.Manager, sessions SessionValidator) gin.HandlerFunc {
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
		if sessions != nil {
			active, validationErr := sessions.IsSessionActive(ctx, domain.UserID(claims.UserID), domain.DeviceID(claims.DeviceID), claims.SessionID, time.Now().UTC())
			if validationErr != nil {
				ctx.Abort()
				httpx.Error(ctx, validationErr)
				return
			}
			if !active {
				ctx.Abort()
				httpx.Error(ctx, domain.ErrUnauthorized)
				return
			}
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
