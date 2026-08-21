package handler

import (
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/application"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/realtime"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/transport/httpx"
	"github.com/gin-gonic/gin"
)

type Set struct {
	services application.Services
	hub      *realtime.Hub
}

func New(services application.Services, hub *realtime.Hub) *Set {
	return &Set{services: services, hub: hub}
}

func (set *Set) Todo(useCase string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// TODO(linknest): bind transport data and invoke the matching application service.
		_ = set.services
		httpx.NotImplemented(ctx, useCase)
	}
}

func (set *Set) Health(ctx *gin.Context) {
	httpx.OK(ctx, gin.H{
		"status":             "ok",
		"service":            "linknest-api",
		"realtime_clients":   set.hub.ConnectionCount(),
		"business_readiness": "scaffold",
	})
}

func (set *Set) Ready(ctx *gin.Context) {
	// TODO(linknest): replace scaffold probes with MySQL, Redis, and RabbitMQ checks.
	httpx.OK(ctx, gin.H{"status": "scaffold", "dependencies": "not-wired"})
}
