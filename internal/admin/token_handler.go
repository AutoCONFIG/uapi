package admin

import (
	"encoding/json"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

// HandleTokens routes token CRUD operations by HTTP method.
func (h *Handler) HandleTokens(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	switch method {
	case "GET":
		h.listTokens(ctx)
	case "POST":
		h.createToken(ctx)
	case "PUT":
		h.updateToken(ctx)
	case "DELETE":
		h.deleteToken(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listTokens(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Token
	h.db.Model(&db.Token{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: items,
	})
}

func (h *Handler) createToken(ctx *fasthttp.RequestCtx) {
	var req CreateTokenRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" || req.Key == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name and key are required")
		return
	}
	t := db.Token{
		Name:        req.Name,
		Key:         req.Key,
		Enabled:     true,
		IPWhitelist: req.IPWhitelist,
		Unlimited:   req.Unlimited,
	}
	t.ID = uuid.New()
	if err := h.db.Create(&t).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	auditCreate(h.db, "token", t.ID, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, t)
}

func (h *Handler) updateToken(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdateTokenRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.Token
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Key != nil {
		updates["key"] = *req.Key
	}
	if req.IPWhitelist != nil {
		updates["ip_whitelist"] = *req.IPWhitelist
	}
	if req.Unlimited != nil {
		updates["unlimited"] = *req.Unlimited
	}
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing)
	auditUpdate(h.db, "token", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deleteToken(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.Token{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now)
	if result.Error != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	if result.RowsAffected == 0 {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	auditDelete(h.db, "token", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}
