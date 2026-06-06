package admin

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AutoCONFIG/uapi/internal/appsettings"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
)

type AdminSettingsResponse struct {
	LogRetentionDays        int    `json:"log_retention_days"`
	RedeemCodeRetentionDays int    `json:"redeem_code_retention_days"`
	SoftDeleteRetentionDays int    `json:"soft_delete_retention_days"`
	ModelRatios             string `json:"model_ratios"`
	AdminUsername           string `json:"admin_username"`
	MaxKeysPerUser          int    `json:"max_keys_per_user"`
	Background              string `json:"background"`
	PublicBaseURL           string `json:"public_base_url,omitempty"`
	WallpaperURL            string `json:"wallpaper_url,omitempty"`
	LargePayloadThresholdMB int    `json:"large_payload_threshold_mb"`
}

type UpdateAdminSettingsRequest struct {
	LogRetentionDays        *int    `json:"log_retention_days"`
	RedeemCodeRetentionDays *int    `json:"redeem_code_retention_days"`
	SoftDeleteRetentionDays *int    `json:"soft_delete_retention_days"`
	ModelRatios             *string `json:"model_ratios"`
	AdminUsername           *string `json:"admin_username"`
	AdminPassword           *string `json:"admin_password"`
	MaxKeysPerUser          *int    `json:"max_keys_per_user"`
	Background              *string `json:"background"`
	PublicBaseURL           *string `json:"public_base_url"`
	LargePayloadThresholdMB *int    `json:"large_payload_threshold_mb"`
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
			changes["log_retention_days"] = *req.LogRetentionDays
		}
		if req.RedeemCodeRetentionDays != nil {
			if *req.RedeemCodeRetentionDays <= 0 {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "redeem_code_retention_days must be greater than 0")
				return
			}
			changes["redeem_code_retention_days"] = *req.RedeemCodeRetentionDays
		}
		if req.SoftDeleteRetentionDays != nil {
			if *req.SoftDeleteRetentionDays <= 0 {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "soft_delete_retention_days must be greater than 0")
				return
			}
			changes["soft_delete_retention_days"] = *req.SoftDeleteRetentionDays
		}
		if req.ModelRatios != nil {
			modelRatios, msg := normalizeModelRatios(*req.ModelRatios)
			if msg != "" {
				h.jsonError(ctx, fasthttp.StatusBadRequest, msg)
				return
			}
			changes["model_ratios"] = modelRatios
		}
		if req.AdminUsername != nil {
			adminUsername := strings.TrimSpace(*req.AdminUsername)
			if adminUsername == "" {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "admin_username is required")
				return
			}
			changes["admin_username"] = adminUsername
		}
		if req.AdminPassword != nil {
			adminPassword := strings.TrimSpace(*req.AdminPassword)
			if adminPassword != "" {
				if len(adminPassword) < 8 {
					h.jsonError(ctx, fasthttp.StatusBadRequest, "admin_password must be at least 8 characters")
					return
				}
				hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
				if err != nil {
					h.jsonError(ctx, fasthttp.StatusInternalServerError, "hash password failed")
					return
				}
				changes["admin_password_hash"] = string(hash)
			}
		}
		if req.MaxKeysPerUser != nil {
			if *req.MaxKeysPerUser < 0 {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "max_keys_per_user must be greater than or equal to 0")
				return
			}
			changes["max_keys_per_user"] = *req.MaxKeysPerUser
		}
		if req.Background != nil {
			background := normalizeBackground(*req.Background)
			if background == "" {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "unsupported background")
				return
			}
			changes["background"] = background
		}
		if req.PublicBaseURL != nil {
			publicBaseURL, ok := normalizePublicBaseURL(*req.PublicBaseURL)
			if !ok {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "public_base_url must be a valid http or https URL")
				return
			}
			changes["public_base_url"] = publicBaseURL
		}
		if req.LargePayloadThresholdMB != nil {
			if *req.LargePayloadThresholdMB < 1 || *req.LargePayloadThresholdMB > 1024 {
				h.jsonError(ctx, fasthttp.StatusBadRequest, "large_payload_threshold_mb must be between 1 and 1024")
				return
			}
			changes["large_payload_threshold_mb"] = *req.LargePayloadThresholdMB
		}
		if len(changes) == 0 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "no fields to update")
			return
		}
		if err := h.saveSettings(changes); err != nil {
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
	background := normalizeBackground(appsettings.Get(h.db, appsettings.UIBackground, "mesh"))
	if background == "" {
		background = "mesh"
	}
	modelRatios := appsettings.Get(h.db, appsettings.ModelRatios, "{}")
	if modelRatios == "" {
		modelRatios = "{}"
	}
	return AdminSettingsResponse{
		LogRetentionDays:        appsettings.GetInt(h.db, appsettings.LogRetentionDays, 180),
		RedeemCodeRetentionDays: appsettings.GetInt(h.db, appsettings.RedeemCodeRetentionDays, 180),
		SoftDeleteRetentionDays: appsettings.GetInt(h.db, appsettings.SoftDeleteRetentionDays, 30),
		ModelRatios:             modelRatios,
		AdminUsername:           appsettings.Get(h.db, appsettings.AdminUsername, "admin"),
		MaxKeysPerUser:          appsettings.GetInt(h.db, appsettings.UserMaxKeysPerUser, 1),
		Background:              background,
		PublicBaseURL:           appsettings.Get(h.db, appsettings.UIPublicBaseURL, ""),
		WallpaperURL:            h.wallpaperURL(),
		LargePayloadThresholdMB: appsettings.GetInt(h.db, appsettings.LargePayloadThresholdMB, 256),
	}
}

func (h *Handler) HandlePublicSettings(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "GET" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	background := normalizeBackground(appsettings.Get(h.db, appsettings.UIBackground, "mesh"))
	if background == "" {
		background = "mesh"
	}
	h.jsonResponse(ctx, 200, PublicSettingsResponse{Background: background, PublicBaseURL: appsettings.Get(h.db, appsettings.UIPublicBaseURL, ""), WallpaperURL: h.wallpaperURL()})
}

func (h *Handler) saveSettings(changes map[string]interface{}) error {
	values := make(map[string]string, len(changes))
	for key, value := range changes {
		switch key {
		case "log_retention_days":
			values[appsettings.LogRetentionDays] = strconv.Itoa(value.(int))
		case "redeem_code_retention_days":
			values[appsettings.RedeemCodeRetentionDays] = strconv.Itoa(value.(int))
		case "soft_delete_retention_days":
			values[appsettings.SoftDeleteRetentionDays] = strconv.Itoa(value.(int))
		case "model_ratios":
			values[appsettings.ModelRatios] = value.(string)
		case "admin_username":
			values[appsettings.AdminUsername] = value.(string)
		case "admin_password_hash":
			values[appsettings.AdminPasswordHash] = value.(string)
		case "max_keys_per_user":
			values[appsettings.UserMaxKeysPerUser] = strconv.Itoa(value.(int))
		case "background":
			values[appsettings.UIBackground] = value.(string)
		case "public_base_url":
			values[appsettings.UIPublicBaseURL] = value.(string)
		case "large_payload_threshold_mb":
			values[appsettings.LargePayloadThresholdMB] = strconv.Itoa(value.(int))
		}
	}
	return appsettings.SetMany(h.db, values)
}

func normalizeBackground(value string) string {
	switch value {
	case "", "aurora", "silk", "mesh", "topography", "noir", "custom":
		return "mesh"
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
	if err := appsettings.SetMany(h.db, map[string]string{
		appsettings.UIBackground:    "mesh",
		appsettings.UIWallpaperPath: target,
	}); err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "save settings failed")
		return
	}
	createAuditLogWithValues(h.db, "update", "settings", "wallpaper", h.getAdminUser(ctx), auditIP(ctx), "", auditJSON(map[string]interface{}{"background": "mesh"}))
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
	path := appsettings.Get(h.db, appsettings.UIWallpaperPath, "")
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
	path := appsettings.Get(h.db, appsettings.UIWallpaperPath, "")
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
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
