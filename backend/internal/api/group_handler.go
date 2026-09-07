package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"my-im/internal/apperror"
	"my-im/internal/service"
)

type GroupHandler struct{ groups service.GroupCoreService }

func NewGroupHandler(groups service.GroupCoreService) *GroupHandler {
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

func (h *GroupHandler) AddMember(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	groupID, ok := groupIDParam(c)
	if !ok {
		return
	}
	var request AddGroupMemberRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.MemberID <= 0 {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	if err := h.groups.AddMember(c.Request.Context(), groupID, userID, request.MemberID); err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func (h *GroupHandler) RemoveMember(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	groupID, ok := groupIDParam(c)
	if !ok {
		return
	}
	memberID, ok := positivePathID(c, "memberID")
	if !ok {
		return
	}
	if err := h.groups.RemoveMember(c.Request.Context(), groupID, userID, memberID); err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func (h *GroupHandler) ListMembers(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	groupID, ok := groupIDParam(c)
	if !ok {
		return
	}
	limit, offset, ok := pagination(c)
	if !ok {
		return
	}
	page, err := h.groups.ListMembers(c.Request.Context(), groupID, userID, limit, offset)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, pageResponse(page))
}

func (h *GroupHandler) UpdateMemberRole(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	groupID, ok := groupIDParam(c)
	if !ok {
		return
	}
	memberID, ok := positivePathID(c, "memberID")
	if !ok {
		return
	}
	var request UpdateGroupMemberRoleRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.Role == nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	if err := h.groups.UpdateRole(c.Request.Context(), groupID, userID, memberID, *request.Role); err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func (h *GroupHandler) MuteMember(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	groupID, ok := groupIDParam(c)
	if !ok {
		return
	}
	memberID, ok := positivePathID(c, "memberID")
	if !ok {
		return
	}
	var request MuteGroupMemberRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.MutedUntil == nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	if err := h.groups.MuteMember(c.Request.Context(), groupID, userID, memberID, request.MutedUntil); err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func (h *GroupHandler) UnmuteMember(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	groupID, ok := groupIDParam(c)
	if !ok {
		return
	}
	memberID, ok := positivePathID(c, "memberID")
	if !ok {
		return
	}
	if err := h.groups.MuteMember(c.Request.Context(), groupID, userID, memberID, nil); err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func groupIDParam(c *gin.Context) (int64, bool) {
	return positivePathID(c, "groupID")
}

func positivePathID(c *gin.Context, name string) (int64, bool) {
	value, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || value <= 0 {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return 0, false
	}
	return value, true
}
