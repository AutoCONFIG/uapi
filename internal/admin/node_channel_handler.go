package admin

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

func (h *Handler) HandleNodeChannels(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "GET":
		h.listNodeChannels(ctx)
	case "POST":
		h.createNodeChannel(ctx)
	case "PUT":
		h.updateNodeChannel(ctx)
	case "DELETE":
		h.deleteNodeChannel(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listNodeChannels(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	query := h.db.Model(&db.NodeChannel{}).Where("deleted_at IS NULL")
	if nodeID := string(ctx.QueryArgs().Peek("relay_node_id")); nodeID != "" {
		if _, err := uuid.Parse(nodeID); err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid relay_node_id")
			return
		}
		query = query.Where("relay_node_id = ?", nodeID)
	}
	if channelID := string(ctx.QueryArgs().Peek("channel_id")); channelID != "" {
		if _, err := uuid.Parse(channelID); err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid channel_id")
			return
		}
		query = query.Where("channel_id = ?", channelID)
	}
	var total int64
	var items []db.NodeChannel
	query.Count(&total)
	query.Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{Total: total, Page: page, Limit: limit, Items: items})
}

func (h *Handler) createNodeChannel(ctx *fasthttp.RequestCtx) {
	var req CreateNodeChannelRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.RelayNodeID == uuid.Nil || req.ChannelID == uuid.Nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "relay_node_id and channel_id are required")
		return
	}
	if req.Weight < 0 || req.Weight > 10000 {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "weight must be between 0 and 10000")
		return
	}
	if !h.existsRelayNode(req.RelayNodeID) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "relay node not found")
		return
	}
	if !h.existsChannel(req.ChannelID) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "channel not found")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	binding := db.NodeChannel{RelayNodeID: req.RelayNodeID, ChannelID: req.ChannelID, Weight: req.Weight, Enabled: enabled}
	binding.ID = uuid.New()
	if err := h.db.Create(&binding).Error; err != nil {
		if strings.Contains(err.Error(), "idx_node_channel_active") || strings.Contains(err.Error(), "duplicate key") {
			h.jsonError(ctx, fasthttp.StatusConflict, "node already bound to this channel")
			return
		}
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	auditCreateCtx(h.db, "node_channel", binding.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"relay_node_id": binding.RelayNodeID, "channel_id": binding.ChannelID, "weight": binding.Weight})
	h.jsonResponse(ctx, 200, binding)
}

func (h *Handler) updateNodeChannel(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdateNodeChannelRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.NodeChannel
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{"updated_at": time.Now()}
	if req.RelayNodeID != nil {
		if *req.RelayNodeID == uuid.Nil || !h.existsRelayNode(*req.RelayNodeID) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "relay node not found")
			return
		}
		updates["relay_node_id"] = *req.RelayNodeID
	}
	if req.ChannelID != nil {
		if *req.ChannelID == uuid.Nil || !h.existsChannel(*req.ChannelID) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "channel not found")
			return
		}
		updates["channel_id"] = *req.ChannelID
	}
	if req.Weight != nil {
		if *req.Weight < 0 || *req.Weight > 10000 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "weight must be between 0 and 10000")
			return
		}
		updates["weight"] = *req.Weight
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "reload failed")
		return
	}
	auditUpdateCtx(h.db, "node_channel", id, h.getAdminUser(ctx), ctx, updates)
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deleteNodeChannel(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.NodeChannel{}).Where("id = ? AND deleted_at IS NULL", id).Updates(map[string]interface{}{
		"enabled":    false,
		"deleted_at": now,
	})
	if result.Error != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	if result.RowsAffected == 0 {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	auditDeleteCtx(h.db, "node_channel", id, h.getAdminUser(ctx), ctx, nil)
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

func (h *Handler) existsRelayNode(id uuid.UUID) bool {
	var count int64
	h.db.Model(&db.RelayNode{}).Where("id = ? AND deleted_at IS NULL", id).Count(&count)
	return count > 0
}

func (h *Handler) existsChannel(id uuid.UUID) bool {
	var count int64
	h.db.Model(&db.Channel{}).Where("id = ? AND deleted_at IS NULL", id).Count(&count)
	return count > 0
}
