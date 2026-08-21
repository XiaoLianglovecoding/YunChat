package websocket

import (
	"net/http"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/realtime"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/httpx"
	"github.com/gin-gonic/gin"
	ws "github.com/gorilla/websocket"
)

type Handler struct {
	hub      *realtime.Hub
	upgrader ws.Upgrader
}

func New(hub *realtime.Hub) *Handler {
	return &Handler{
		hub: hub,
		upgrader: ws.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(*http.Request) bool {
				// TODO(linknest): validate Origin against configured browser origins before upgrading.
				return false
			},
		},
	}
}

func (handler *Handler) Serve(ctx *gin.Context) {
	// TODO(linknest): authenticate the first frame, upgrade, register a client, and run read/write pumps.
	_ = handler.hub
	_ = handler.upgrader
	httpx.NotImplemented(ctx, "realtime.connect")
}
