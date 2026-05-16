package admin

import (
	"encoding/json"
	"time"

	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

// ListUsers returns a paginated list of users.
func (h *Handler) ListUsers(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "GET" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit

	var total int64
	var users []db.User
	query := h.db.Model(&db.User{}).Where("deleted_at IS NULL")

	// Optional status filter
	if status := string(ctx.QueryArgs().Peek("status")); status != "" {
		query = query.Where("status = ?", status)
	}

	query.Count(&total)
	query.Order("created_at desc").Limit(limit).Offset(offset).Find(&users)

	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: users,
	})
}

// UpdateUser updates a user's status and/or balance.
func (h *Handler) UpdateUser(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "PUT" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdateUserRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "disabled" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid status: must be 'active' or 'disabled'")
		return
	}
	var existing db.User
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.Balance != nil {
		updates["balance"] = *req.Balance
	}
	if len(updates) == 0 {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "no fields to update")
		return
	}
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing)
	auditUpdate(h.db, "user", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, existing)
}

// DeleteUser soft-deletes a user.
func (h *Handler) DeleteUser(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "DELETE" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.User{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now)
	if result.Error != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	if result.RowsAffected == 0 {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	auditDelete(h.db, "user", id, h.getAdminUser(ctx))
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}
