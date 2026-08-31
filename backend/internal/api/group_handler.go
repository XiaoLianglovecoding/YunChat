package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"my-im/internal/apperror"
	"my-im/internal/service"
)

type GroupHandler struct{ groups service.GroupProfileService }

func NewGroupHandler(groups service.GroupProfileService) *GroupHandler {
	return &GroupHandler{groups: groups}
}

func (h *GroupHandler) Create(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	var request CreateGroupRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	groupID, err := h.groups.Create(c.Request.Context(), userID, request.Name, request.Notice)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusCreated, CreateGroupResponse{GroupID: groupID})
}

func (h *GroupHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	groups, err := h.groups.ListByUser(c.Request.Context(), userID)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, groups)
}

func (h *GroupHandler) Get(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	groupID, ok := groupIDParam(c)
	if !ok {
		return
	}
	group, err := h.groups.Get(c.Request.Context(), userID, groupID)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, group)
}

func (h *GroupHandler) Update(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	groupID, ok := groupIDParam(c)
	if !ok {
		return
	}
	var request UpdateGroupRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	if err := h.groups.Update(c.Request.Context(), userID, groupID, request.Name, request.Notice); err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func groupIDParam(c *gin.Context) (int64, bool) {
	groupID, err := strconv.ParseInt(c.Param("groupID"), 10, 64)
	if err != nil || groupID <= 0 {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return 0, false
	}
	return groupID, true
}
