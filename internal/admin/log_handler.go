package admin

import (
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/valyala/fasthttp"
)

// HandleLogs returns a paginated list of request logs.
func (h *Handler) HandleLogs(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "GET" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Log
	h.db.Model(&db.Log{}).Count(&total)
	h.db.Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: items,
	})
}

// ListAuditLogs returns a paginated list of audit logs.
func (h *Handler) ListAuditLogs(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "GET" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.AuditLog

	query := h.db.Model(&db.AuditLog{})

	// Optional filters
	if action := string(ctx.QueryArgs().Peek("action")); action != "" {
		query = query.Where("action = ?", action)
	}
	if resource := string(ctx.QueryArgs().Peek("resource")); resource != "" {
		query = query.Where("resource = ?", resource)
	}

	query.Count(&total)
	query.Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: items,
	})
}
