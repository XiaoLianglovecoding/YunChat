package api

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"my-im/internal/apperror"
	"my-im/internal/middleware"
	"my-im/internal/service"
)

type AvatarHandler struct {
	profile   service.ProfileService
	upload    service.UploadService
	maxSizeMB int
}

func NewAvatarHandler(profile service.ProfileService, upload service.UploadService, maxSizeMB int) *AvatarHandler {
	if maxSizeMB <= 0 {
		maxSizeMB = 10
	}
	return &AvatarHandler{profile: profile, upload: upload, maxSizeMB: maxSizeMB}
}

// GetAvatar 重定向到已上传文件；未上传时返回一个稳定、无外部依赖的 SVG 占位头像。
func (h *AvatarHandler) GetAvatar(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("userID"), 10, 64)
	if err != nil || userID <= 0 {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	profile, err := h.profile.GetAvatar(c.Request.Context(), userID)
	if err != nil {
		WriteError(c, err)
		return
	}
	if strings.HasPrefix(profile.AvatarURL, "/uploads/avatars/") && !strings.Contains(profile.AvatarURL, "..") && !strings.Contains(profile.AvatarURL, "\\") {
		c.Redirect(http.StatusFound, profile.AvatarURL)
		return
	}
	initial, _ := utf8.DecodeRuneInString(profile.Username)
	label := strings.ToUpper(string(initial))
	if initial == utf8.RuneError || label == "" {
		label = "?"
	}
	colors := []string{"#F44336", "#7E57C2", "#3F51B5", "#039BE5", "#00897B", "#43A047", "#FB8C00", "#6D4C41"}
	color := colors[int(userID)%len(colors)]
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="200" viewBox="0 0 200 200"><rect width="200" height="200" fill="%s"/><text x="100" y="132" font-family="Arial,sans-serif" font-size="92" font-weight="bold" fill="#fff" text-anchor="middle">%s</text></svg>`, color, html.EscapeString(label))
	c.Header("Content-Type", "image/svg+xml; charset=utf-8")
	c.Header("Cache-Control", "public, max-age=300")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	c.String(http.StatusOK, svg)
}

func (h *AvatarHandler) UploadAvatar(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		WriteError(c, apperror.New(apperror.CodeUnauthorized))
		return
	}
	maxBytes := int64(h.maxSizeMB)*1024*1024 + 1024*1024 // multipart 边界和请求头预留 1 MiB。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			WriteError(c, apperror.WithMessage(apperror.CodeInvalidParam, "avatar request body is too large"))
			return
		}
		WriteError(c, apperror.WithMessage(apperror.CodeMissingParam, "multipart field 'file' is required"))
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		WriteError(c, apperror.New(apperror.CodeInvalidParam))
		return
	}
	defer file.Close()
	result, err := h.upload.UploadAvatar(c.Request.Context(), int64(userID), fileHeader.Filename, file)
	if err != nil {
		WriteError(c, err)
		return
	}
	WriteSuccess(c, http.StatusOK, UploadAvatarResponse{URL: result.URL, FilePath: result.FilePath, Size: result.Size})
}
