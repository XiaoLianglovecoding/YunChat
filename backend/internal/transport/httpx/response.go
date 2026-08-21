package httpx

import (
	"errors"
	"net/http"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
	"github.com/gin-gonic/gin"
)

const RequestIDKey = "request_id"

type Envelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data,omitempty"`
	RequestID string `json:"request_id"`
}

func OK(ctx *gin.Context, data any) {
	ctx.JSON(http.StatusOK, Envelope{Code: "OK", Message: "success", Data: data, RequestID: requestID(ctx)})
}

func Created(ctx *gin.Context, data any) {
	ctx.JSON(http.StatusCreated, Envelope{Code: "CREATED", Message: "created", Data: data, RequestID: requestID(ctx)})
}

func NotImplemented(ctx *gin.Context, useCase string) {
	ctx.JSON(http.StatusNotImplemented, Envelope{
		Code:      "NOT_IMPLEMENTED",
		Message:   useCase + " is reserved by the scaffold and has not been implemented",
		RequestID: requestID(ctx),
	})
}

func Error(ctx *gin.Context, err error) {
	status, code, message := classify(err)
	ctx.JSON(status, Envelope{Code: code, Message: message, RequestID: requestID(ctx)})
}

func classify(err error) (int, string, string) {
	switch {
	case errors.Is(err, domain.ErrInvalidArgument):
		return http.StatusBadRequest, "INVALID_ARGUMENT", err.Error()
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized, "UNAUTHORIZED", "authentication is required"
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "FORBIDDEN", "operation is not allowed"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "NOT_FOUND", "resource was not found"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "CONFLICT", err.Error()
	case errors.Is(err, domain.ErrNotImplemented):
		return http.StatusNotImplemented, "NOT_IMPLEMENTED", err.Error()
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR", "an internal error occurred"
	}
}

func requestID(ctx *gin.Context) string {
	return ctx.GetString(RequestIDKey)
}
