package admin

import (
	"encoding/json"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

func (h *Handler) HandleNodeAccounts(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "GET":
		h.listNodeAccounts(ctx)
	case "POST":
		h.createNodeAccount(ctx)
	case "PUT":
		h.updateNodeAccount(ctx)
	case "DELETE":
		h.deleteNodeAccount(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listNodeAccounts(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	query := h.db.Model(&db.NodeAccount{}).Where("deleted_at IS NULL")
	if nodeID := string(ctx.QueryArgs().Peek("relay_node_id")); nodeID != "" {
		if _, err := uuid.Parse(nodeID); err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid relay_node_id")
			return
		}
		query = query.Where("relay_node_id = ?", nodeID)
	}
	if accountID := string(ctx.QueryArgs().Peek("account_id")); accountID != "" {
		if _, err := uuid.Parse(accountID); err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid account_id")
			return
		}
		query = query.Where("account_id = ?", accountID)
	}
	var total int64
	var items []db.NodeAccount
	query.Count(&total)
	query.Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{Total: total, Page: page, Limit: limit, Items: items})
}

func (h *Handler) createNodeAccount(ctx *fasthttp.RequestCtx) {
	var req CreateNodeAccountRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.RelayNodeID == uuid.Nil || req.AccountID == uuid.Nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "relay_node_id and account_id are required")
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
	if !h.existsAccount(req.AccountID) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "account not found")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	binding := db.NodeAccount{RelayNodeID: req.RelayNodeID, AccountID: req.AccountID, Weight: req.Weight, Enabled: enabled}
	if binding.Weight == 0 {
		binding.Weight = 100
	}
	binding.ID = uuid.New()
	if err := h.db.Create(&binding).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	auditCreate(h.db, "node_account", binding.ID, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, binding)
}

func (h *Handler) updateNodeAccount(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdateNodeAccountRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.NodeAccount
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
	if req.AccountID != nil {
		if *req.AccountID == uuid.Nil || !h.existsAccount(*req.AccountID) {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "account not found")
			return
		}
		updates["account_id"] = *req.AccountID
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
	auditUpdate(h.db, "node_account", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deleteNodeAccount(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.NodeAccount{}).Where("id = ? AND deleted_at IS NULL", id).Updates(map[string]interface{}{
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
	auditDelete(h.db, "node_account", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

func (h *Handler) existsRelayNode(id uuid.UUID) bool {
	var count int64
	h.db.Model(&db.RelayNode{}).Where("id = ? AND deleted_at IS NULL", id).Count(&count)
	return count > 0
}

func (h *Handler) existsAccount(id uuid.UUID) bool {
	var count int64
	h.db.Model(&db.Account{}).Where("id = ? AND deleted_at IS NULL", id).Count(&count)
	return count > 0
}
