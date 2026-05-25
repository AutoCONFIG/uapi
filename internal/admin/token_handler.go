package admin

import (
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
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
	for i := range items {
		items[i].Key = ""
	}
	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: items,
	})
}

func (h *Handler) createToken(ctx *fasthttp.RequestCtx) {
	h.jsonError(ctx, fasthttp.StatusForbidden, "admin token creation is disabled; users create their own API keys")
}

func (h *Handler) updateToken(ctx *fasthttp.RequestCtx) {
	h.jsonError(ctx, fasthttp.StatusForbidden, "admin token updates are disabled; manage access through plans")
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
	auditDeleteCtx(h.db, "token", id, h.getAdminUser(ctx), ctx, nil)
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}
