package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"my-im/internal/apperror"
	authtoken "my-im/internal/auth"
)

type UserID int64

const (
	UserIDKey   = "auth_user_id"
	UsernameKey = "auth_username"
)

type AccessTokenVerifier interface {
	ParseAccess(string) (*authtoken.Claims, error)
}

func RequireAuth(verifier AccessTokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := strings.TrimSpace(c.GetHeader("Authorization"))
		if header == "" {
			abortAuth(c, apperror.CodeUnauthorized)
			return
		}
		parts := strings.Fields(header)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			abortAuth(c, apperror.CodeInvalidToken)
			return
		}
		claims, err := verifier.ParseAccess(parts[1])
		if err != nil {
			abortAuth(c, apperror.CodeInvalidToken)
			return
		}
		c.Set(UserIDKey, UserID(claims.UserID))
		c.Set(UsernameKey, claims.Username)
		c.Next()
	}
}

// RequireAuthUnavailable 用于依赖尚未装配的测试/工具进程；它仍然保持保护路由关闭。
func RequireAuthUnavailable() gin.HandlerFunc {
	return func(c *gin.Context) {
		abortAuth(c, apperror.CodeUnauthorized)
	}
}

func CurrentUserID(c *gin.Context) (UserID, bool) {
	value, ok := c.Get(UserIDKey)
	if !ok {
		return 0, false
	}
	userID, ok := value.(UserID)
	return userID, ok && userID > 0
}

func abortAuth(c *gin.Context, code apperror.Code) {
	err := apperror.New(code)
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": int(err.Code), "message": err.Message})
}
