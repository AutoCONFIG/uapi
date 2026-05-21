package admin

import (
	"encoding/json"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

// HandleAccounts routes account CRUD operations by HTTP method.
func (h *Handler) HandleAccounts(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	switch method {
	case "GET":
		h.listAccounts(ctx)
	case "POST":
		h.createAccount(ctx)
	case "PUT":
		h.updateAccount(ctx)
	case "DELETE":
		h.deleteAccount(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listAccounts(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Account
	h.db.Model(&db.Account{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: items,
	})
}

func (h *Handler) createAccount(ctx *fasthttp.RequestCtx) {
	var req CreateAccountRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" || req.ChannelID == uuid.Nil || req.Credentials == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name, channel_id and credentials are required")
		return
	}
	encrypted, err := crypto.Encrypt(req.Credentials)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt failed")
		return
	}
	acc := db.Account{
		ChannelID:   req.ChannelID,
		Name:        req.Name,
		Credentials: encrypted,
		Weight:      req.Weight,
		Enabled:     true,
	}
	acc.ID = uuid.New()
	if err := h.db.Create(&acc).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	if h.RefreshPool != nil {
		h.RefreshPool(acc.ChannelID.String())
	}
	if h.OAuthIdle != nil {
		h.OAuthIdle.ScheduleAccount(&acc)
	}
	auditCreate(h.db, "account", acc.ID, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, acc)
}

func (h *Handler) updateAccount(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdateAccountRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.Account
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	originalChannelID := existing.ChannelID
	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.ChannelID != uuid.Nil {
		updates["channel_id"] = req.ChannelID
	}
	if req.Credentials != "" {
		encrypted, err := crypto.Encrypt(req.Credentials)
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "encrypt failed")
			return
		}
		updates["credentials"] = encrypted
	}
	if req.Weight != nil {
		updates["weight"] = *req.Weight
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.CooldownUntil != nil {
		updates["cooldown_until"] = req.CooldownUntil
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
		if originalChannelID != existing.ChannelID {
			// Channel changed: refresh both old and new channel pools
			h.RefreshPool(originalChannelID.String())
			h.RefreshPool(existing.ChannelID.String())
		} else {
			h.RefreshPool(existing.ChannelID.String())
		}
	}
	if h.OAuthIdle != nil {
		h.OAuthIdle.ScheduleAccount(&existing)
	}
	auditUpdate(h.db, "account", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deleteAccount(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.Account{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now)
	if result.Error != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	if result.RowsAffected == 0 {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	if h.OAuthIdle != nil {
		h.OAuthIdle.CancelAccount(id)
	}
	if h.RefreshPool != nil {
		// Find the channel this account belonged to so we can refresh its pool
		var acc db.Account
		if h.db.Unscoped().Where("id = ?", id).First(&acc).Error == nil {
			h.RefreshPool(acc.ChannelID.String())
		}
	}
	auditDelete(h.db, "account", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}
