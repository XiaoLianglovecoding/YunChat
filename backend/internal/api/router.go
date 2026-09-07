package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"my-im/internal/middleware"
	"my-im/internal/observability"
	"my-im/internal/service"
)

type ReadinessCheck func(context.Context) map[string]error

type RouterOptions struct {
	ServiceName    string
	WSPath         string
	UploadDir      string
	FrontendDir    string
	AllowedOrigins []string
	Readiness      ReadinessCheck
	Logger         *zap.Logger
	Metrics        *observability.Metrics
	MetricsPath    string
	Auth           service.AuthService
	TokenVerifier  middleware.AccessTokenVerifier
	Profile        service.ProfileService
	Upload         service.UploadService
	Friend         service.FriendService
	Group          service.GroupCoreService
	WebSocket      gin.HandlerFunc
	FileMaxSizeMB  int
}

// TodoRoute 是从原项目路由表提取出的业务入口。
type TodoRoute struct {
	Method  string
	Path    string
	TaskID  string
	Feature string
}

var publicRoutes = []TodoRoute{
	{http.MethodPost, "/auth/register", "AUTH-001", "用户注册"},
	{http.MethodPost, "/auth/login", "AUTH-002", "用户登录与令牌签发"},
	{http.MethodPost, "/auth/refresh", "AUTH-003", "刷新访问令牌"},
	{http.MethodGet, "/avatar/:userID", "PROFILE-001", "读取用户头像"},
}

var protectedRoutes = []TodoRoute{
	{http.MethodPut, "/account/username", "AUTH-005", "修改用户名"},
	{http.MethodPut, "/account/password", "AUTH-006", "修改密码"},

	{http.MethodPost, "/friend/request", "FRIEND-001", "发送好友申请"},
	{http.MethodPost, "/friend/accept", "FRIEND-002", "接受好友申请"},
	{http.MethodPost, "/friend/reject", "FRIEND-002", "拒绝好友申请"},
	{http.MethodGet, "/friend/requests", "FRIEND-001", "好友申请列表"},
	{http.MethodGet, "/friend/list", "FRIEND-003", "好友列表"},
	{http.MethodDelete, "/friend/:friendID", "FRIEND-003", "删除好友"},
	{http.MethodPost, "/friend/block", "FRIEND-004", "拉黑用户"},
	{http.MethodPost, "/friend/unblock", "FRIEND-004", "取消拉黑"},

	{http.MethodPost, "/group", "GROUP-001", "创建群组"},
	{http.MethodGet, "/group/list", "GROUP-001", "我的群组列表"},
	{http.MethodPut, "/group/:groupID", "GROUP-001", "更新群资料"},
	{http.MethodGet, "/group/:groupID", "GROUP-001", "群资料详情"},
	{http.MethodPost, "/group/:groupID/member", "GROUP-002", "添加群成员"},
	{http.MethodDelete, "/group/:groupID/member/:memberID", "GROUP-002", "移除群成员"},
	{http.MethodGet, "/group/:groupID/members", "GROUP-002", "群成员列表"},
	{http.MethodPut, "/group/:groupID/member/:memberID/role", "GROUP-003", "修改成员角色"},
	{http.MethodPut, "/group/:groupID/owner", "GROUP-004", "转让群主"},
	{http.MethodPost, "/group/:groupID/leave", "GROUP-004", "退出群组"},

	{http.MethodPost, "/moment", "MOMENT-001", "发布动态"},
	{http.MethodGet, "/moment/:momentID", "MOMENT-001", "动态详情"},
	{http.MethodGet, "/moment/user/:userID", "MOMENT-001", "用户动态列表"},
	{http.MethodPost, "/moment/:momentID/like", "MOMENT-003", "点赞动态"},
	{http.MethodDelete, "/moment/:momentID/like", "MOMENT-003", "取消点赞"},
	{http.MethodGet, "/moment/:momentID/likers", "MOMENT-003", "点赞用户列表"},
	{http.MethodDelete, "/moment/:momentID", "MOMENT-001", "删除动态"},
	{http.MethodPost, "/moment/:momentID/comment", "MOMENT-004", "评论动态"},
	{http.MethodDelete, "/moment/comment/:commentID", "MOMENT-004", "删除评论"},
	{http.MethodGet, "/moment/feed", "MOMENT-002", "动态 Feed"},

	{http.MethodPost, "/msg/revoke", "MSG-005", "撤回消息"},
	{http.MethodDelete, "/msg/:msgID", "MSG-006", "删除消息"},
	{http.MethodGet, "/msg/search", "MSG-006", "搜索私聊消息"},

	{http.MethodGet, "/settings", "SETTINGS-001", "读取用户设置"},
	{http.MethodPut, "/settings", "SETTINGS-001", "更新用户设置"},
	{http.MethodPost, "/settings/mute", "SETTINGS-002", "静音会话"},
	{http.MethodDelete, "/settings/mute/:convID", "SETTINGS-002", "取消静音会话"},

	{http.MethodPost, "/upload/avatar", "UPLOAD-001", "上传头像"},
}

func BusinessRoutes() []TodoRoute {
	routes := make([]TodoRoute, 0, len(publicRoutes)+len(protectedRoutes))
	routes = append(routes, publicRoutes...)
	routes = append(routes, protectedRoutes...)
	return routes
}

func NewRouter(opts RouterOptions) *gin.Engine {
	if opts.ServiceName == "" {
		opts.ServiceName = "my-im"
	}
	if opts.WSPath == "" {
		opts.WSPath = "/ws"
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.Metrics == nil {
		opts.Metrics = observability.NewMetrics()
	}
	if opts.MetricsPath == "" {
		opts.MetricsPath = "/metrics"
	}

	r := gin.New()
	r.Use(middleware.RequestLog(opts.Logger, opts.Metrics), gin.Recovery(), middleware.CORS(opts.AllowedOrigins))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": opts.ServiceName})
	})
	r.GET("/ready", func(c *gin.Context) {
		if opts.Readiness == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "service": opts.ServiceName})
			return
		}
		report := opts.Readiness(c.Request.Context())
		dependencies := make(map[string]string, len(report))
		ready := true
		for name, err := range report {
			if err != nil {
				dependencies[name] = "error"
				ready = false
				opts.Logger.Warn("readiness_check_failed", zap.String("dependency", name), zap.Error(err))
			} else {
				dependencies[name] = "ok"
			}
		}
		status := http.StatusOK
		label := "ready"
		if !ready {
			status = http.StatusServiceUnavailable
			label = "not_ready"
		}
		c.JSON(status, gin.H{"status": label, "service": opts.ServiceName, "dependencies": dependencies})
	})
	r.GET(opts.MetricsPath, gin.WrapH(opts.Metrics))

	handlers := make(map[string]gin.HandlerFunc)
	if opts.Auth != nil {
		authHandler := NewAuthHandler(opts.Auth)
		handlers[routeKey(http.MethodPost, "/auth/register")] = authHandler.Register
		handlers[routeKey(http.MethodPost, "/auth/login")] = authHandler.Login
		handlers[routeKey(http.MethodPost, "/auth/refresh")] = authHandler.Refresh
		handlers[routeKey(http.MethodPut, "/account/username")] = authHandler.UpdateUsername
		handlers[routeKey(http.MethodPut, "/account/password")] = authHandler.UpdatePassword
	}
	avatarHandler := NewAvatarHandler(opts.Profile, opts.Upload, opts.FileMaxSizeMB)
	if opts.Profile != nil {
		handlers[routeKey(http.MethodGet, "/avatar/:userID")] = avatarHandler.GetAvatar
	}
	if opts.Upload != nil {
		handlers[routeKey(http.MethodPost, "/upload/avatar")] = avatarHandler.UploadAvatar
	}
	if opts.Friend != nil {
		friendHandler := NewFriendHandler(opts.Friend)
		handlers[routeKey(http.MethodPost, "/friend/request")] = friendHandler.SendRequest
		handlers[routeKey(http.MethodPost, "/friend/accept")] = friendHandler.AcceptRequest
		handlers[routeKey(http.MethodPost, "/friend/reject")] = friendHandler.RejectRequest
		handlers[routeKey(http.MethodGet, "/friend/requests")] = friendHandler.ListRequests
		handlers[routeKey(http.MethodGet, "/friend/list")] = friendHandler.ListFriends
		handlers[routeKey(http.MethodDelete, "/friend/:friendID")] = friendHandler.DeleteFriend
		handlers[routeKey(http.MethodPost, "/friend/block")] = friendHandler.Block
		handlers[routeKey(http.MethodPost, "/friend/unblock")] = friendHandler.Unblock
	}
	if opts.Group != nil {
		groupHandler := NewGroupHandler(opts.Group)
		handlers[routeKey(http.MethodPost, "/group")] = groupHandler.Create
		handlers[routeKey(http.MethodGet, "/group/list")] = groupHandler.List
		handlers[routeKey(http.MethodGet, "/group/:groupID")] = groupHandler.Get
		handlers[routeKey(http.MethodPut, "/group/:groupID")] = groupHandler.Update
		handlers[routeKey(http.MethodPost, "/group/:groupID/member")] = groupHandler.AddMember
		handlers[routeKey(http.MethodDelete, "/group/:groupID/member/:memberID")] = groupHandler.RemoveMember
		handlers[routeKey(http.MethodGet, "/group/:groupID/members")] = groupHandler.ListMembers
	}

	v1 := r.Group("/api/v1")
	registerBusinessRoutes(v1, publicRoutes, handlers)

	protected := r.Group("/api/v1")
	if opts.TokenVerifier == nil {
		protected.Use(middleware.RequireAuthUnavailable())
	} else {
		protected.Use(middleware.RequireAuth(opts.TokenVerifier))
	}
	registerBusinessRoutes(protected, protectedRoutes, handlers)

	if opts.WebSocket != nil {
		r.GET(opts.WSPath, opts.WebSocket)
	} else {
		r.GET(opts.WSPath, func(c *gin.Context) {
			TODO(c, "WS-001", "WebSocket 鉴权、升级、连接生命周期与消息分发")
		})
	}

	if opts.UploadDir != "" {
		if info, err := os.Stat(opts.UploadDir); err == nil && info.IsDir() {
			r.Static("/uploads", opts.UploadDir)
		}
	}
	registerSPA(r, opts.FrontendDir)
	return r
}

func registerBusinessRoutes(group *gin.RouterGroup, routes []TodoRoute, handlers map[string]gin.HandlerFunc) {
	for _, route := range routes {
		route := route
		if handler, ok := handlers[routeKey(route.Method, route.Path)]; ok {
			group.Handle(route.Method, route.Path, handler)
			continue
		}
		group.Handle(route.Method, route.Path, func(c *gin.Context) {
			TODO(c, route.TaskID, route.Feature)
		})
	}
}

func routeKey(method, path string) string { return method + " " + path }

func registerSPA(r *gin.Engine, frontendDir string) {
	if frontendDir == "" {
		return
	}
	indexPath := filepath.Join(frontendDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		return
	}

	assetsDir := filepath.Join(frontendDir, "assets")
	if info, err := os.Stat(assetsDir); err == nil && info.IsDir() {
		r.Static("/assets", assetsDir)
	}
	r.GET("/", func(c *gin.Context) {
		c.File(indexPath)
	})
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/ws") || strings.HasPrefix(path, "/uploads/") {
			c.JSON(http.StatusNotFound, Response{Code: 404, Message: "not found"})
			return
		}
		c.File(indexPath)
	})
}
