package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"my-im/internal/apperror"
	"my-im/internal/middleware"
	"my-im/internal/service"
)

const (
	defaultPageLimit = 20
	maxPageLimit     = 100
)

type FriendHandler struct{ friends service.FriendService }

func NewFriendHandler(friends service.FriendService) *FriendHandler {
	return &FriendHandler{friends: friends}
}

func (h *FriendHandler) SendRequest(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	var request SendFriendRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.ToUserID <= 0 {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	result, err := h.friends.SendRequest(c.Request.Context(), userID, request.ToUserID, request.Message)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusCreated, SendFriendResponse{
		RequestID: result.ID, FromUserID: result.FromUserID, ToUserID: result.ToUserID, Status: result.Status,
	})
}

func (h *FriendHandler) ListRequests(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	limit, offset, ok := pagination(c)
	if !ok {
		return
	}
	page, err := h.friends.ListRequests(c.Request.Context(), userID, limit, offset)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, pageResponse(page))
}

func (h *FriendHandler) AcceptRequest(c *gin.Context) {
	h.handleRequestAction(c, true)
}

func (h *FriendHandler) RejectRequest(c *gin.Context) {
	h.handleRequestAction(c, false)
}

func (h *FriendHandler) handleRequestAction(c *gin.Context, accept bool) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	var request FriendRequestAction
	if err := c.ShouldBindJSON(&request); err != nil || request.RequestID <= 0 {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	if accept {
		result, err := h.friends.AcceptRequest(c.Request.Context(), userID, request.RequestID)
		if err != nil {
			WriteError(c, err)
			return
		}
		WriteSuccess(c, http.StatusOK, result)
		return
	}
	if err := h.friends.RejectRequest(c.Request.Context(), userID, request.RequestID); err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func (h *FriendHandler) ListFriends(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	limit, offset, ok := pagination(c)
	if !ok {
		return
	}
	page, err := h.friends.ListFriends(c.Request.Context(), userID, limit, offset)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, pageResponse(page))
}

func (h *FriendHandler) DeleteFriend(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	friendID, err := strconv.ParseInt(c.Param("friendID"), 10, 64)
	if err != nil || friendID <= 0 {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	if err := h.friends.DeleteFriend(c.Request.Context(), userID, friendID); err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func (h *FriendHandler) Block(c *gin.Context)   { h.handleBlock(c, true) }
func (h *FriendHandler) Unblock(c *gin.Context) { h.handleBlock(c, false) }

func (h *FriendHandler) handleBlock(c *gin.Context, block bool) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	var request BlockUserRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.BlockedID <= 0 {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	var err error
	if block {
		err = h.friends.Block(c.Request.Context(), userID, request.BlockedID)
	} else {
		err = h.friends.Unblock(c.Request.Context(), userID, request.BlockedID)
	}
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func currentUserID(c *gin.Context) (int64, bool) {
	value, ok := middleware.CurrentUserID(c)
	if !ok {
		WriteError(c, apperror.New(apperror.CodeUnauthorized))
		return 0, false
	}
	return int64(value), true
}

func pagination(c *gin.Context) (int, int, bool) {
	limit, err := parseNonNegativeQuery(c, "limit", defaultPageLimit)
	if err != nil || limit == 0 || limit > maxPageLimit {
		WriteError(c, apperror.WithMessage(apperror.CodeInvalidParam, "limit must be between 1 and 100"))
		return 0, 0, false
	}
	offset, err := parseNonNegativeQuery(c, "offset", 0)
	if err != nil {
		WriteError(c, apperror.WithMessage(apperror.CodeInvalidParam, "offset must be a non-negative integer"))
		return 0, 0, false
	}
	return limit, offset, true
}

func parseNonNegativeQuery(c *gin.Context, name string, fallback int) (int, error) {
	raw, exists := c.GetQuery(name)
	if !exists || raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func pageResponse[T any](page service.Page[T]) PageResponse[T] {
	items := page.Items
	if items == nil {
		items = make([]T, 0)
	}
	return PageResponse[T]{
		Items: items,
		Pagination: PaginationResponse{
			Total: page.Total, Offset: page.Offset, Limit: page.Limit,
			HasMore: int64(page.Offset+len(page.Items)) < page.Total,
		},
	}
}
