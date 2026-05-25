package admin

import (
	"encoding/json"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

// HandlePlans routes plan CRUD operations by HTTP method.
func (h *Handler) HandlePlans(ctx *fasthttp.RequestCtx) {
	method := string(ctx.Method())
	switch method {
	case "GET":
		h.listPlans(ctx)
	case "POST":
		h.createPlan(ctx)
	case "PUT":
		h.updatePlan(ctx)
	case "DELETE":
		h.deletePlan(ctx)
	default:
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listPlans(ctx *fasthttp.RequestCtx) {
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	var total int64
	var items []db.Plan
	h.db.Model(&db.Plan{}).Where("deleted_at IS NULL").Count(&total)
	h.db.Where("deleted_at IS NULL").Order("created_at desc").Limit(limit).Offset(offset).Find(&items)
	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: items,
	})
}

func (h *Handler) createPlan(ctx *fasthttp.RequestCtx) {
	var req CreatePlanRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.Name == "" || req.Type == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "name and type are required")
		return
	}
	durationDays := req.DurationDays
	if durationDays <= 0 {
		durationDays = 30
	}
	p := db.Plan{
		Name:            req.Name,
		Type:            req.Type,
		PolicyID:        req.PolicyID,
		Limits:          req.Limits,
		ModelRatios:     req.ModelRatios,
		CompletionRatio: req.CompletionRatio,
		TokenQuota:      req.TokenQuota,
		Enabled:         req.Enabled,
		DurationDays:    durationDays,
	}
	p.ID = uuid.New()
	if err := h.db.Create(&p).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	auditCreateCtx(h.db, "plan", p.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"name": p.Name, "type": p.Type, "token_quota": p.TokenQuota, "policy_id": p.PolicyID, "duration_days": p.DurationDays})
	h.jsonResponse(ctx, 200, p)
}

func (h *Handler) updatePlan(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	var req UpdatePlanRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	var existing db.Plan
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Type != nil {
		updates["type"] = *req.Type
	}
	if req.PolicyID != nil {
		updates["policy_id"] = *req.PolicyID
	}
	if req.Limits != nil {
		updates["limits"] = *req.Limits
	}
	if req.ModelRatios != nil {
		updates["model_ratios"] = *req.ModelRatios
	}
	if req.CompletionRatio != nil {
		updates["completion_ratio"] = *req.CompletionRatio
	}
	if req.TokenQuota != nil {
		updates["token_quota"] = *req.TokenQuota
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.DurationDays != nil {
		if *req.DurationDays <= 0 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "duration_days must be greater than 0")
			return
		}
		updates["duration_days"] = *req.DurationDays
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
	auditUpdateCtx(h.db, "plan", id, h.getAdminUser(ctx), ctx, updates)
	h.jsonResponse(ctx, 200, existing)
}

func (h *Handler) deletePlan(ctx *fasthttp.RequestCtx) {
	idStr := string(ctx.QueryArgs().Peek("id"))
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid id")
		return
	}
	now := time.Now()
	result := h.db.Model(&db.Plan{}).Where("id = ? AND deleted_at IS NULL", id).Update("deleted_at", now)
	if result.Error != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "delete failed")
		return
	}
	if result.RowsAffected == 0 {
		h.jsonError(ctx, fasthttp.StatusNotFound, "not found")
		return
	}
	auditDeleteCtx(h.db, "plan", id, h.getAdminUser(ctx), ctx, nil)
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}
