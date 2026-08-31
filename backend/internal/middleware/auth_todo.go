package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireAuthTODO 默认关闭所有受保护业务端点，防止在 JWT 校验尚未实现时误开放接口。
func RequireAuthTODO() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
			"code":    1900,
			"message": "TODO[AUTH-004]: 实现 JWT 解析、签名校验、过期校验与用户上下文注入",
			"data": gin.H{
				"task_id": "AUTH-004",
				"feature": "JWT 鉴权中间件",
			},
		})
	}
}
