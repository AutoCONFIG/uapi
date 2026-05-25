package admin

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/config"
	"github.com/valyala/fasthttp"
)

type AdminSettingsResponse struct {
	LogRetentionDays        int    `json:"log_retention_days"`
	RedeemCodeRetentionDays int    `json:"redeem_code_retention_days"`
	Background              string `json:"background"`
	PublicBaseURL           string `json:"public_base_url,omitempty"`
	WallpaperURL            string `json:"wallpaper_url,omitempty"`
}

type UpdateAdminSettingsRequest struct {
	LogRetentionDays        *int    `json:"log_retention_days"`
	RedeemCodeRetentionDays *int    `json:"redeem_code_retention_days"`
	Background              *string `json:"background"`
	PublicBaseURL           *string `json:"public_base_url"`
}

type PublicSettingsResponse struct {
	Background    string `json:"background"`
	PublicBaseURL string `json:"public_base_url,omitempty"`
	WallpaperURL  string `json:"wallpaper_url,omitempty"`
}

func (h *Handler) HandleSettings(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "GET":
		h.jsonResponse(ctx, 200, h.settingsResponse())
	case "PUT":
		var req UpdateAdminSettingsRequest
		if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
			return
		}
		changes := map[string]interface{}{}
		if req.LogRetentionDays != nil {
			if *req.LogRetentionDays <= 0 {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "log_retention_days must be greater than 0")
				return
			}
			h.cfg.Logging.RetentionDays = *req.LogRetentionDays
			changes["log_retention_days"] = *req.LogRetentionDays
		}
		if req.RedeemCodeRetentionDays != nil {
			if *req.RedeemCodeRetentionDays <= 0 {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "redeem_code_retention_days must be greater than 0")
				return
			}
			h.cfg.Logging.RedeemCodeRetentionDays = *req.RedeemCodeRetentionDays
			changes["redeem_code_retention_days"] = *req.RedeemCodeRetentionDays
		}
		if req.Background != nil {
			background := normalizeBackground(*req.Background)
			if background == "" {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "unsupported background")
				return
			}
			h.cfg.UI.Background = background
			changes["background"] = background
		}
		if req.PublicBaseURL != nil {
			publicBaseURL, ok := normalizePublicBaseURL(*req.PublicBaseURL)
			if !ok {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "public_base_url must be a valid http or https URL")
				return
			}
			h.cfg.UI.PublicBaseURL = publicBaseURL
			changes["public_base_url"] = publicBaseURL
		}
		if len(changes) == 0 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "no fields to update")
			return
		}
		if err := config.Save(h.cfg, h.cfgPath); err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "save settings failed")
			return
		}
		createAuditLogWithValues(h.db, "update", "settings", "logging", h.getAdminUser(ctx), auditIP(ctx), "", auditJSON(changes))
		h.jsonResponse(ctx, 200, h.settingsResponse())
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) settingsResponse() AdminSettingsResponse {
	background := normalizeBackground(h.cfg.UI.Background)
	if background == "" {
		background = "aurora"
	}
	return AdminSettingsResponse{
		LogRetentionDays:        h.cfg.Logging.RetentionDays,
		RedeemCodeRetentionDays: h.cfg.Logging.RedeemCodeRetentionDays,
		Background:              background,
		PublicBaseURL:           h.cfg.UI.PublicBaseURL,
		WallpaperURL:            h.wallpaperURL(),
	}
}

func (h *Handler) HandlePublicSettings(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "GET" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	background := normalizeBackground(h.cfg.UI.Background)
	if background == "" {
		background = "aurora"
	}
	h.jsonResponse(ctx, 200, PublicSettingsResponse{Background: background, PublicBaseURL: h.cfg.UI.PublicBaseURL, WallpaperURL: h.wallpaperURL()})
}

func normalizeBackground(value string) string {
	switch value {
	case "", "aurora":
		return "aurora"
	case "silk", "mesh", "topography", "noir", "custom":
		return value
	default:
		return ""
	}
}

func normalizePublicBaseURL(value string) (string, bool) {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
	if trimmed == "" {
		return "", true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	return trimmed, true
}

func (h *Handler) HandleWallpaperUpload(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "POST" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	form, err := ctx.MultipartForm()
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid multipart form")
		return
	}
	files := form.File["file"]
	if len(files) == 0 {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "file is required")
		return
	}
	file := files[0]
	if file.Size > 8*1024*1024 {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "wallpaper must be 8MB or smaller")
		return
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	contentType := wallpaperContentType(ext)
	if contentType == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "only jpg, png, webp, or gif wallpapers are supported")
		return
	}
	dir := filepath.Join(filepath.Dir(h.cfgPath), "assets")
	if err := os.MkdirAll(dir, 0700); err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create wallpaper directory failed")
		return
	}
	target := filepath.Join(dir, "wallpaper"+ext)
	if err := fasthttp.SaveMultipartFile(file, target); err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "save wallpaper failed")
		return
	}
	cleanupOldWallpapers(dir, target)
	h.cfg.UI.Background = "custom"
	h.cfg.UI.WallpaperPath = target
	if err := config.Save(h.cfg, h.cfgPath); err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "save settings failed")
		return
	}
	createAuditLogWithValues(h.db, "update", "settings", "wallpaper", h.getAdminUser(ctx), auditIP(ctx), "", auditJSON(map[string]interface{}{"background": "custom"}))
	h.jsonResponse(ctx, 200, h.settingsResponse())
}

func cleanupOldWallpapers(dir, keep string) {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif"} {
		path := filepath.Join(dir, "wallpaper"+ext)
		if path != keep {
			_ = os.Remove(path)
		}
	}
}

func (h *Handler) HandlePublicWallpaper(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "GET" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	path := h.cfg.UI.WallpaperPath
	if path == "" {
		h.jsonError(ctx, fasthttp.StatusNotFound, "wallpaper not configured")
		return
	}
	ext := strings.ToLower(filepath.Ext(path))
	contentType := wallpaperContentType(ext)
	if contentType == "" {
		h.jsonError(ctx, fasthttp.StatusNotFound, "wallpaper not configured")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "wallpaper not found")
		return
	}
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetContentType(contentType)
	ctx.SetStatusCode(200)
	ctx.SetBody(data)
}

func (h *Handler) wallpaperURL() string {
	if h.cfg.UI.WallpaperPath == "" {
		return ""
	}
	info, err := os.Stat(h.cfg.UI.WallpaperPath)
	if err != nil {
		return ""
	}
	return "/api/public/wallpaper?v=" + fmt.Sprint(info.ModTime().Unix())
}

func wallpaperContentType(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return ""
	}
}
