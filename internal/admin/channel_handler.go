package admin

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

// HandleChannels routes channel CRUD operations by HTTP method.
func (h *Handler) HandleChannels(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	switch method {
	case "GET":
		h.listChannels(ctx)
	case "POST":
		h.createChannel(ctx)
	case "PUT":
		h.updateChannel(ctx)
	case "DELETE":
		h.deleteChannel(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func normalizeChannelGroup(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	return value
}

func (h *Handler) listChannels(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Channel
	h.db.Model(&db.Channel{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: items,
	})
}

func (h *Handler) createChannel(ctx *fasthttp.RequestCtx) {
	var req CreateChannelRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" || req.Type == "" || req.Endpoint == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name, type and endpoint are required")
		return
	}
	if !validChannelFormatForType(req.Type, req.APIFormat) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "channel type and api_format are incompatible")
		return
	}
	ch := db.Channel{
		Name:        req.Name,
		Type:        req.Type,
		Group:       normalizeChannelGroup(req.Group),
		Endpoint:    req.Endpoint,
		Models:      req.Models,
		Priority:    req.Priority,
		APIFormat:   req.APIFormat,
		ForceStream: req.ForceStream,
		AffinityTTL: req.AffinityTTL,
		Enabled:     true,
	}
	ch.ID = uuid.New()
	if err := h.db.Create(&ch).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	if h.RefreshPool != nil {
		h.RefreshPool(ch.ID.String())
	}
	auditCreate(h.db, "channel", ch.ID, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, ch)
}

func (h *Handler) updateChannel(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdateChannelRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.Channel
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	targetType := existing.Type
	targetAPIFormat := existing.APIFormat
	if req.Type != nil {
		targetType = *req.Type
	}
	if req.APIFormat != nil {
		targetAPIFormat = *req.APIFormat
	}
	if !validChannelFormatForType(targetType, targetAPIFormat) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "channel type and api_format are incompatible")
		return
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Type != nil {
		updates["type"] = *req.Type
	}
	if req.Group != nil {
		updates["channel_group"] = normalizeChannelGroup(*req.Group)
	}
	if req.Endpoint != nil {
		updates["endpoint"] = *req.Endpoint
	}
	if req.Models != nil {
		updates["models"] = *req.Models
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.APIFormat != nil {
		if ok, msg := h.channelAccountsCompatibleWithAPIFormat(existing.ID, *req.APIFormat); !ok {
			h.jsonError(ctx, fasthttp.StatusBadRequest, msg)
			return
		}
		updates["api_format"] = *req.APIFormat
	}
	if req.ForceStream != nil {
		updates["force_stream"] = *req.ForceStream
	}
	if req.AffinityTTL != nil {
		updates["affinity_ttl"] = *req.AffinityTTL
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "reload failed")
		return
	}
	if h.RefreshPool != nil {
		h.RefreshPool(existing.ID.String())
	}
	auditUpdate(h.db, "channel", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) channelAccountsCompatibleWithAPIFormat(channelID uuid.UUID, apiFormat string) (bool, string) {
	var accounts []db.Account
	if err := h.db.Where("channel_id = ? AND deleted_at IS NULL", channelID).Find(&accounts).Error; err != nil {
		return false, "load accounts failed"
	}
	if !isCodeAPIFormat(apiFormat) {
		for _, acc := range accounts {
			if codeOAuthAccountRequiresCodeChannel(acc) {
				return false, "Code OAuth credentials can only be assigned to Code channels"
			}
		}
		return true, ""
	}
	target := db.Channel{APIFormat: apiFormat}
	for _, acc := range accounts {
		if acc.CredType != "oauth_token" {
			return false, "Code channels require OAuth credentials"
		}
		if !oauthAccountMatchesCodeChannel(acc, target) {
			return false, "OAuth credential provider does not match Code channel"
		}
	}
	return true, ""
}

func validChannelFormatForType(channelType, apiFormat string) bool {
	switch channelType {
	case "openai":
		return apiFormat == "" || apiFormat == "standard" || apiFormat == "responses" || apiFormat == "codex"
	case "gemini":
		return apiFormat == "" || apiFormat == "standard" || apiFormat == "gemini_code"
	case "anthropic":
		return apiFormat == "" || apiFormat == "standard" || apiFormat == "claude_code"
	default:
		return false
	}
}

func (h *Handler) deleteChannel(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.Channel{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now)
	if result.Error != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	if result.RowsAffected == 0 {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	if h.RemovePool != nil {
		h.RemovePool(id.String())
	}
	auditDelete(h.db, "channel", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}
