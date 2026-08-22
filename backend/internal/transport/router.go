package transport

import (
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/application"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/config"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/realtime"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/handler"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/middleware"
	websockettransport "github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/websocket"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/token"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func NewRouter(cfg config.Config, log *zap.Logger, tokens *token.Manager, sessions middleware.SessionValidator, limiter middleware.RateLimiter, services application.Services, hub *realtime.Hub) *gin.Engine {
	if cfg.App.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(middleware.RequestID(), middleware.CORS(cfg.Server.AllowedOrigins), middleware.Recovery(log))

	handlers := handler.New(services, hub)
	router.GET("/healthz", handlers.Health)
	router.GET("/readyz", handlers.Ready)
	router.GET(cfg.Server.WebSocketPath, websockettransport.New(hub).Serve)

	api := router.Group("/api/v1")
	auth := api.Group("/auth")
	{
		auth.POST("/register", middleware.RateLimit(limiter, "register", cfg.Auth.RegisterLimit, cfg.Auth.RegisterWindow), handlers.Register)
		auth.POST("/login", middleware.RateLimit(limiter, "login", cfg.Auth.LoginLimit, cfg.Auth.LoginWindow), handlers.Login)
		auth.POST("/refresh", handlers.Refresh)
	}

	protected := api.Group("")
	protected.Use(middleware.RequireAuth(tokens, sessions))
	{
		protected.POST("/auth/logout", handlers.Logout)
		protected.POST("/auth/change-password", handlers.ChangePassword)
		protected.GET("/users/me", handlers.GetMe)
		protected.PATCH("/users/me", handlers.UpdateMe)
		protected.GET("/users/me/settings", handlers.GetSettings)
		protected.PATCH("/users/me/settings", handlers.UpdateSettings)
		protected.GET("/users/:id", handlers.Todo("user.get_public_profile"))

		protected.GET("/friend-requests", handlers.Todo("contact.list_requests"))
		protected.POST("/friend-requests", handlers.Todo("contact.send_request"))
		protected.POST("/friend-requests/:id/accept", handlers.Todo("contact.accept_request"))
		protected.POST("/friend-requests/:id/reject", handlers.Todo("contact.reject_request"))
		protected.GET("/contacts", handlers.Todo("contact.list"))
		protected.DELETE("/contacts/:id", handlers.Todo("contact.delete"))
		protected.PUT("/blocks/:user_id", handlers.Todo("contact.block"))
		protected.DELETE("/blocks/:user_id", handlers.Todo("contact.unblock"))

		protected.GET("/conversations", handlers.Todo("conversation.list"))
		protected.POST("/conversations", handlers.Todo("conversation.create_direct"))
		protected.GET("/conversations/:id/messages", handlers.Todo("message.list"))
		protected.POST("/conversations/:id/read", handlers.Todo("conversation.mark_read"))
		protected.POST("/messages/:id/recall", handlers.Todo("message.recall"))

		protected.POST("/groups", handlers.Todo("group.create"))
		protected.PATCH("/groups/:id", handlers.Todo("group.update"))
		protected.GET("/groups/:id/members", handlers.Todo("group.list_members"))
		protected.POST("/groups/:id/members", handlers.Todo("group.add_members"))
		protected.DELETE("/groups/:id/members/:user_id", handlers.Todo("group.remove_member"))
		protected.POST("/uploads/presign", handlers.Todo("upload.presign"))
	}

	return router
}
