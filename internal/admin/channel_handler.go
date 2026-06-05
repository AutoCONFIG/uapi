package admin

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/upstreamconfig"
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
		return "默认渠道"
	}
	return value
}

func (h *Handler) listChannels(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Channel
	h.db.Model(&db.Channel{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("priority DESC, weight DESC, created_at DESC").Limit(limit).Offset(offset).Find(&items)
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
	if req.Name == "" || req.Type == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name and type are required")
		return
	}
	if !validChannelFormatForType(req.Type, req.APIFormat) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "channel type and api_format are incompatible")
		return
	}
	ch := db.Channel{
		Name:         req.Name,
		Type:         req.Type,
		Group:        normalizeChannelGroup(req.Group),
		Endpoint:     channelEndpointOrDefault(req.Type, req.APIFormat),
		Models:       req.Models,
		ModelAliases: req.ModelAliases,
		Priority:     req.Priority,
		Weight:       normalizeChannelWeight(req.Weight),
		APIFormat:    req.APIFormat,
		ForceStream:  req.ForceStream,
		AffinityTTL:  req.AffinityTTL,
		Settings:     normalizeChannelSettings(req.Settings),
		Enabled:      true,
	}
	ch.ID = uuid.New()
	if err := h.db.Create(&ch).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	if h.RefreshPool != nil {
		h.RefreshPool(ch.ID.String())
	}
	auditCreateCtx(h.db, "channel", ch.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"name": ch.Name, "type": ch.Type, "group": ch.Group, "api_format": ch.APIFormat})
	h.jsonResponse(ctx, 200, ch)
}

func channelEndpointOrDefault(providerType, apiFormat string) string {
	return upstreamconfig.DefaultEndpoint(providerType, apiFormat)
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
	if req.Models != nil {
		updates["models"] = *req.Models
	}
	if req.ModelAliases != nil {
		updates["model_aliases"] = *req.ModelAliases
	}
	if req.Priority != nil {
		updates["priority"] = *req.Priority
	}
	if req.Weight != nil {
		updates["weight"] = normalizeChannelWeight(*req.Weight)
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
	if req.Settings != nil {
		updates["settings"] = normalizeChannelSettings(*req.Settings)
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
	auditUpdateCtx(h.db, "channel", id, h.getAdminUser(ctx), ctx, updates)
	h.jsonResponse(ctx, 200, existing)
}

func normalizeChannelWeight(value int) int {
	if value <= 0 {
		return 100
	}
	if value > 10000 {
		return 10000
	}
	return value
}

func normalizeChannelSettings(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return "{}"
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func (h *Handler) channelAccountsCompatibleWithAPIFormat(channelID uuid.UUID, apiFormat string) (bool, string) {
	var accounts []db.Account
	if err := h.db.Where("channel_id = ? AND deleted_at IS NULL", channelID).Find(&accounts).Error; err != nil {
		return false, "load accounts failed"
	}
	if !isOAuthAPIFormat(apiFormat) && !isReverseAPIFormat(apiFormat) {
		for _, acc := range accounts {
			if oauthAccountRequiresOAuthChannel(acc) {
				return false, "OAuth credentials can only be assigned to OAuth channels"
			}
			if reverseAccountRequiresReverseChannel(acc) {
				return false, "Reverse credentials can only be assigned to reverse channels"
			}
		}
		return true, ""
	}
	if isReverseAPIFormat(apiFormat) {
		for _, acc := range accounts {
			if acc.CredType != "chatgpt_reverse" {
				return false, "Reverse channels require reverse credentials"
			}
		}
		return true, ""
	}
	target := db.Channel{APIFormat: apiFormat}
	for _, acc := range accounts {
		if acc.CredType != "oauth_token" {
			return false, "OAuth channels require OAuth credentials"
		}
		if !oauthAccountMatchesOAuthChannel(acc, target) {
			return false, "OAuth credential provider does not match OAuth channel"
		}
	}
	return true, ""
}

func validChannelFormatForType(channelType, apiFormat string) bool {
	switch channelType {
	case "openai":
		return apiFormat == "" || apiFormat == "standard" || apiFormat == "responses" || apiFormat == "codex" || apiFormat == "chatgpt_reverse"
	case "gemini":
		return apiFormat == "" || apiFormat == "standard" || apiFormat == "gemini_code"
	case "anthropic":
		return apiFormat == "" || apiFormat == "standard" || apiFormat == "claude_code"
	case "antigravity":
		return apiFormat == "antigravity"
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
	auditDeleteCtx(h.db, "channel", id, h.getAdminUser(ctx), ctx, nil)
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}
