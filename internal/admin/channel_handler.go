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

const DefaultOAuthChannelAffinityTTL = 600

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
		AffinityTTL:  affinityTTLOrDefault(req.AffinityTTL, req.Type, req.APIFormat),
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
	targetType, targetAPIFormat := resolveChannelTypeAndAPIFormat(existing.Type, existing.APIFormat, req.Type, req.APIFormat)
	if !validChannelFormatForType(targetType, targetAPIFormat) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "channel type and api_format are incompatible")
		return
	}
	if req.Type != nil && *req.Type != existing.Type {
		if ok, msg := h.channelAccountsCompatibleWithTypeChange(existing.ID, targetAPIFormat); !ok {
			h.jsonError(ctx, fasthttp.StatusBadRequest, msg)
			return
		}
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
	if req.APIFormat != nil || targetAPIFormat != existing.APIFormat {
		if ok, msg := h.channelAccountsCompatibleWithAPIFormat(existing.ID, targetAPIFormat); !ok {
			h.jsonError(ctx, fasthttp.StatusBadRequest, msg)
			return
		}
		updates["api_format"] = targetAPIFormat
	}
	if req.ForceStream != nil {
		updates["force_stream"] = *req.ForceStream
	}
	if req.AffinityTTL != nil {
		updates["affinity_ttl"] = normalizeAffinityTTL(*req.AffinityTTL)
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

func affinityTTLOrDefault(value *int, channelType, apiFormat string) int {
	if value != nil {
		return normalizeAffinityTTL(*value)
	}
	return defaultAffinityTTLForChannel(channelType, apiFormat)
}

func defaultAffinityTTLForChannel(channelType, apiFormat string) int {
	return DefaultOAuthChannelAffinityTTL
}

func normalizeAffinityTTL(value int) int {
	if value <= 0 {
		return 0
	}
	if value > 86400 {
		return 86400
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

func (h *Handler) channelAccountsCompatibleWithTypeChange(channelID uuid.UUID, apiFormat string) (bool, string) {
	if !isAPIKeyAPIFormat(apiFormat) {
		return false, "Channel type can only be changed for API Key channels"
	}
	var accounts []db.Account
	if err := h.db.Where("channel_id = ? AND deleted_at IS NULL", channelID).Find(&accounts).Error; err != nil {
		return false, "load accounts failed"
	}
	for _, acc := range accounts {
		if acc.CredType != "api_key" {
			return false, "Channel type can only be changed when all accounts are API Key credentials"
		}
	}
	return true, ""
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

func isAPIKeyAPIFormat(apiFormat string) bool {
	return apiFormat == "" || apiFormat == "standard" || apiFormat == "responses"
}

func resolveChannelTypeAndAPIFormat(existingType, existingAPIFormat string, requestedType, requestedAPIFormat *string) (string, string) {
	targetType := existingType
	targetAPIFormat := existingAPIFormat
	if requestedType != nil {
		targetType = *requestedType
	}
	if requestedAPIFormat != nil {
		targetAPIFormat = *requestedAPIFormat
	} else if requestedType != nil && *requestedType != existingType && isAPIKeyAPIFormat(targetAPIFormat) && !validChannelFormatForType(targetType, targetAPIFormat) {
		targetAPIFormat = apiKeyAPIFormatForType(targetType, targetAPIFormat)
	}
	return targetType, targetAPIFormat
}

func apiKeyAPIFormatForType(channelType, currentAPIFormat string) string {
	if validChannelFormatForType(channelType, currentAPIFormat) && isAPIKeyAPIFormat(currentAPIFormat) {
		return currentAPIFormat
	}
	switch channelType {
	case "openai":
		return "standard"
	case "gemini":
		return "standard"
	case "anthropic":
		return "standard"
	default:
		return currentAPIFormat
	}
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

// HandleClearChannelFailure clears channel-model blocks for a channel,
// re-enabling model routing through this channel. Route: POST /api/admin/channels/:id/clear-failure
func (h *Handler) HandleClearChannelFailure(ctx *fasthttp.RequestCtx) {
	idStr := ctx.UserValue("id").(string)
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid channel id")
		return
	}
	// Verify channel exists
	var ch db.Channel
	if h.db.Where("id = ? AND deleted_at IS NULL", id).First(&ch).Error != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "channel not found")
		return
	}
	cleared := 0
	if h.channelModelBlock != nil {
		cleared = h.channelModelBlock.ClearChannel(id.String())
	}
	// Refresh pool in case accounts were affected
	if h.RefreshPool != nil {
		h.RefreshPool(id.String())
	}
	h.jsonResponse(ctx, 200, map[string]interface{}{
		"cleared":    true,
		"channel_id": id.String(),
		"blocks_removed": cleared,
	})
}
