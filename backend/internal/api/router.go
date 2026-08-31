package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/example/my-im/internal/middleware"
)

type RouterOptions struct {
	ServiceName    string
	WSPath         string
	UploadDir      string
	FrontendDir    string
	AllowedOrigins []string
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

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), middleware.CORS(opts.AllowedOrigins))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": opts.ServiceName, "mode": "skeleton"})
	})
	r.GET("/ready", func(c *gin.Context) {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":  "not_ready",
			"service": opts.ServiceName,
			"reason":  "infrastructure and business TODOs are not implemented",
		})
	})

	v1 := r.Group("/api/v1")
	registerTodoRoutes(v1, publicRoutes)

	protected := r.Group("/api/v1")
	protected.Use(middleware.RequireAuthTODO())
	registerTodoRoutes(protected, protectedRoutes)

	r.GET(opts.WSPath, func(c *gin.Context) {
		TODO(c, "WS-001", "WebSocket 鉴权、升级、连接生命周期与消息分发")
	})

	if opts.UploadDir != "" {
		if info, err := os.Stat(opts.UploadDir); err == nil && info.IsDir() {
			r.Static("/uploads", opts.UploadDir)
		}
	}
	registerSPA(r, opts.FrontendDir)
	return r
}

func registerTodoRoutes(group *gin.RouterGroup, routes []TodoRoute) {
	for _, route := range routes {
		route := route
		group.Handle(route.Method, route.Path, func(c *gin.Context) {
			TODO(c, route.TaskID, route.Feature)
		})
	}
}

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
