package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"my-im/internal/apperror"
	"my-im/internal/middleware"
	"my-im/internal/service"
)

type AuthHandler struct{ auth service.AuthService }

func NewAuthHandler(auth service.AuthService) *AuthHandler { return &AuthHandler{auth: auth} }

func (h *AuthHandler) Register(c *gin.Context) {
	var request RegisterRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	result, err := h.auth.Register(c.Request.Context(), service.RegisterCommand{Username: request.Username, Password: request.Password})
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusCreated, RegisterResponse{UserID: result.UserID, Username: result.Username})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var request RegisterRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	result, err := h.auth.Login(c.Request.Context(), service.LoginCommand{Username: request.Username, Password: request.Password})
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, loginResponse(result))
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var request RefreshRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	result, err := h.auth.Refresh(c.Request.Context(), request.RefreshToken)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, loginResponse(result))
}

func (h *AuthHandler) UpdateUsername(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		WriteError(c, apperror.New(apperror.CodeUnauthorized))
		return
	}
	var request UpdateUsernameRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	result, err := h.auth.UpdateUsername(c.Request.Context(), int64(userID), request.Username)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, loginResponse(result))
}

func (h *AuthHandler) UpdatePassword(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		WriteError(c, apperror.New(apperror.CodeUnauthorized))
		return
	}
	var request UpdatePasswordRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	if err := h.auth.UpdatePassword(c.Request.Context(), int64(userID), request.CurrentPassword, request.NewPassword); err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, nil)
}

func loginResponse(result service.TokenPair) LoginResponse {
	return LoginResponse{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, ExpiresIn: result.ExpiresIn, AvatarURL: result.AvatarURL}
}
