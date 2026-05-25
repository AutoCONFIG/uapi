package admin

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

func (h *Handler) HandleAccessPolicies(ctx *fasthttp.RequestCtx) {
	switch string(ctx.Method()) {
	case "GET":
		h.listAccessPolicies(ctx)
	case "POST":
		h.createAccessPolicy(ctx)
	case "PUT":
		h.updateAccessPolicy(ctx)
	case "DELETE":
		h.deleteAccessPolicy(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listAccessPolicies(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.AccessPolicy
	h.db.Model(&db.AccessPolicy{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{Total: total, Page: page, Limit: limit, Items: items})
}

func (h *Handler) createAccessPolicy(ctx *fasthttp.RequestCtx) {
	var req CreateAccessPolicyRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name is required")
		return
	}
	if msg := validatePolicyLimits(req.MaxConcurrency, req.HourlyLimit, req.WeeklyLimit, req.MonthlyLimit); msg != "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, msg)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	policy := db.AccessPolicy{
		Name:           strings.TrimSpace(req.Name),
		AllowedModels:  strings.TrimSpace(req.AllowedModels),
		MaxConcurrency: req.MaxConcurrency,
		HourlyLimit:    req.HourlyLimit,
		WeeklyLimit:    req.WeeklyLimit,
		MonthlyLimit:   req.MonthlyLimit,
		Enabled:        enabled,
	}
	policy.ID = uuid.New()
	if err := h.db.Create(&policy).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	auditCreateCtx(h.db, "access_policy", policy.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"name": policy.Name, "allowed_models": policy.AllowedModels, "max_concurrency": policy.MaxConcurrency})
	h.jsonResponse(ctx, 200, policy)
}

func (h *Handler) updateAccessPolicy(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdateAccessPolicyRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.AccessPolicy
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{"updated_at": time.Now()}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "name is required")
			return
		}
		updates["name"] = name
	}
	if req.AllowedModels != nil {
		updates["allowed_models"] = strings.TrimSpace(*req.AllowedModels)
	}
	if req.MaxConcurrency != nil {
		if *req.MaxConcurrency < 0 || *req.MaxConcurrency > 100000 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "max_concurrency must be between 0 and 100000")
			return
		}
		updates["max_concurrency"] = *req.MaxConcurrency
	}
	if req.HourlyLimit != nil {
		if *req.HourlyLimit < 0 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "hourly_limit must be >= 0")
			return
		}
		updates["hourly_limit"] = *req.HourlyLimit
	}
	if req.WeeklyLimit != nil {
		if *req.WeeklyLimit < 0 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "weekly_limit must be >= 0")
			return
		}
		updates["weekly_limit"] = *req.WeeklyLimit
	}
	if req.MonthlyLimit != nil {
		if *req.MonthlyLimit < 0 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "monthly_limit must be >= 0")
			return
		}
		updates["monthly_limit"] = *req.MonthlyLimit
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
	auditUpdateCtx(h.db, "access_policy", id, h.getAdminUser(ctx), ctx, updates)
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deleteAccessPolicy(ctx *fasthttp.RequestCtx) {
	id, err := uuid.Parse(string(ctx.QueryArgs().Peek("id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.AccessPolicy{}).Where("id = ? AND deleted_at IS NULL", id).Updates(map[string]interface{}{
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
	auditDeleteCtx(h.db, "access_policy", id, h.getAdminUser(ctx), ctx, nil)
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

func validatePolicyLimits(maxConcurrency, hourly, weekly, monthly int) string {
	if maxConcurrency < 0 || maxConcurrency > 100000 {
		return "max_concurrency must be between 0 and 100000"
	}
	if hourly < 0 || weekly < 0 || monthly < 0 {
		return "limits must be >= 0"
	}
	return ""
}
