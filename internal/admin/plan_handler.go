package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
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
	if req.Type != "count_based" && req.Type != "token_based" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "type must be count_based or token_based")
		return
	}
	durationDays := req.DurationDays
	if durationDays <= 0 {
		durationDays = 30
	}
	if msg := validatePolicyLimits(req.MaxConcurrency, req.HourlyLimit, req.WeeklyLimit, req.MonthlyLimit); msg != "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, msg)
		return
	}
	var p db.Plan
	var policy db.AccessPolicy
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		policy = db.AccessPolicy{
			AllowedModels:  strings.TrimSpace(req.AllowedModels),
			MaxConcurrency: req.MaxConcurrency,
			HourlyLimit:    req.HourlyLimit,
			WeeklyLimit:    req.WeeklyLimit,
			MonthlyLimit:   req.MonthlyLimit,
			Enabled:        true,
		}
		policy.ID = uuid.New()
		if err := tx.Create(&policy).Error; err != nil {
			return err
		}
		policyID := policy.ID
		p = db.Plan{
			Name:         req.Name,
			Type:         req.Type,
			PolicyID:     &policyID,
			Enabled:      req.Enabled,
			Public:       req.Public,
			DurationDays: durationDays,
		}
		p.ID = uuid.New()
		return tx.Create(&p).Error
	}); err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "create failed")
		return
	}
	auditCreateCtx(h.db, "plan", p.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"name": p.Name, "type": p.Type, "policy_id": p.PolicyID, "duration_days": p.DurationDays})
	auditCreateCtx(h.db, "access_policy", policy.ID, h.getAdminUser(ctx), ctx, map[string]interface{}{"plan_id": p.ID, "allowed_models": policy.AllowedModels, "max_concurrency": policy.MaxConcurrency})
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
		if *req.Type != "count_based" && *req.Type != "token_based" {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "type must be count_based or token_based")
			return
		}
		updates["type"] = *req.Type
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.Public != nil {
		updates["is_public"] = *req.Public
	}
	if req.DurationDays != nil {
		if *req.DurationDays <= 0 {
			h.jsonError(ctx, fasthttp.StatusBadRequest, "duration_days must be greater than 0")
			return
		}
		updates["duration_days"] = *req.DurationDays
	}
	updates["updated_at"] = time.Now()
	policyChanged := planPolicyChanged(req)
	if policyChanged {
		policy := db.AccessPolicy{
			Enabled: true,
		}
		if existing.PolicyID != nil {
			err := h.db.Where("id = ? AND deleted_at IS NULL", *existing.PolicyID).First(&policy).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				existing.PolicyID = nil
			} else if err != nil {
				h.jsonError(ctx, fasthttp.StatusNotFound, "access policy not found")
				return
			}
		}
		if req.AllowedModels != nil {
			policy.AllowedModels = strings.TrimSpace(*req.AllowedModels)
		}
		if req.MaxConcurrency != nil {
			policy.MaxConcurrency = *req.MaxConcurrency
		}
		if req.HourlyLimit != nil {
			policy.HourlyLimit = *req.HourlyLimit
		}
		if req.WeeklyLimit != nil {
			policy.WeeklyLimit = *req.WeeklyLimit
		}
		if req.MonthlyLimit != nil {
			policy.MonthlyLimit = *req.MonthlyLimit
		}
		if msg := validatePolicyLimits(policy.MaxConcurrency, policy.HourlyLimit, policy.WeeklyLimit, policy.MonthlyLimit); msg != "" {
			h.jsonError(ctx, fasthttp.StatusBadRequest, msg)
			return
		}
	}
	var policyAudit map[string]interface{}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if policyChanged {
			policyUpdates := planPolicyUpdates(req)
			policyUpdates["enabled"] = true
			policyUpdates["updated_at"] = time.Now()
			if existing.PolicyID != nil {
				var policy db.AccessPolicy
				if err := tx.Where("id = ? AND deleted_at IS NULL", *existing.PolicyID).First(&policy).Error; err != nil {
					return err
				}
				if err := tx.Model(&policy).Updates(policyUpdates).Error; err != nil {
					return err
				}
				policyAudit = policyUpdates
			} else {
				policy := db.AccessPolicy{
					AllowedModels:  strings.TrimSpace(valueOrEmpty(req.AllowedModels)),
					MaxConcurrency: valueOrZero(req.MaxConcurrency),
					HourlyLimit:    valueOrZero(req.HourlyLimit),
					WeeklyLimit:    valueOrZero(req.WeeklyLimit),
					MonthlyLimit:   valueOrZero(req.MonthlyLimit),
					Enabled:        true,
				}
				policy.ID = uuid.New()
				if err := tx.Create(&policy).Error; err != nil {
					return err
				}
				policyID := policy.ID
				updates["policy_id"] = policyID
				policyAudit = map[string]interface{}{"created": true, "policy_id": policyID, "allowed_models": policy.AllowedModels, "max_concurrency": policy.MaxConcurrency}
			}
		}
		return tx.Model(&existing).Updates(updates).Error
	}); err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
		return
	}
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "reload failed")
		return
	}
	auditUpdateCtx(h.db, "plan", id, h.getAdminUser(ctx), ctx, updates)
	if policyAudit != nil && existing.PolicyID != nil {
		auditUpdateCtx(h.db, "access_policy", *existing.PolicyID, h.getAdminUser(ctx), ctx, policyAudit)
	}
	h.jsonResponse(ctx, 200, existing)
}

func planPolicyChanged(req UpdatePlanRequest) bool {
	return req.AllowedModels != nil ||
		req.MaxConcurrency != nil ||
		req.HourlyLimit != nil ||
		req.WeeklyLimit != nil ||
		req.MonthlyLimit != nil
}

func planPolicyUpdates(req UpdatePlanRequest) map[string]interface{} {
	updates := map[string]interface{}{}
	if req.AllowedModels != nil {
		updates["allowed_models"] = strings.TrimSpace(*req.AllowedModels)
	}
	if req.MaxConcurrency != nil {
		updates["max_concurrency"] = *req.MaxConcurrency
	}
	if req.HourlyLimit != nil {
		updates["hourly_limit"] = *req.HourlyLimit
	}
	if req.WeeklyLimit != nil {
		updates["weekly_limit"] = *req.WeeklyLimit
	}
	if req.MonthlyLimit != nil {
		updates["monthly_limit"] = *req.MonthlyLimit
	}
	return updates
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func normalizeModelRatios(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}", ""
	}
	var ratios map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &ratios); err != nil {
		return "", "model_ratios must be a JSON object"
	}
	if ratios == nil {
		return "{}", ""
	}
	out := make(map[string]int, len(ratios))
	for model, value := range ratios {
		model = strings.TrimSpace(model)
		if model == "" {
			return "", "model ratio model name cannot be empty"
		}
		ratio, ok := numericRatio(value)
		if !ok || ratio < 0 {
			return "", "model ratio must be a non-negative number"
		}
		rounded := math.Round(ratio)
		if math.Abs(ratio-rounded) > 0.000001 {
			return "", "model ratio must be a non-negative integer"
		}
		out[model] = int(rounded)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Sprintf("encode model ratios: %v", err)
	}
	return string(encoded), ""
}

func numericRatio(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
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
