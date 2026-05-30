package admin

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ListUsers returns a paginated list of users with subscription usage.
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

	if status := string(ctx.QueryArgs().Peek("status")); status != "" {
		query = query.Where("status = ?", status)
	}

	query.Count(&total)
	query.Order("created_at desc").Limit(limit).Offset(offset).Find(&users)

	now := time.Now()
	dtos := make([]userDTO, 0, len(users))
	for _, u := range users {
		dto := userDTOFromUser(u)
		// Load active subscription
		var tp db.TokenPlan
		if err := h.db.Where("user_id = ? AND starts_at <= ? AND expires_at > ?", u.ID.String(), now, now).
			Order("created_at DESC").First(&tp).Error; err == nil {
			var plan db.Plan
			if err := h.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", tp.PlanID).First(&plan).Error; err == nil {
				dto.PlanName = plan.Name
				dto.PlanType = plan.Type
				dto.PlanStartsAt = tp.StartsAt.Format(time.RFC3339)
				dto.PlanExpiresAt = tp.ExpiresAt.Format(time.RFC3339)
				dto.UsageWindows = h.userUsageWindows(u.ID.String(), plan.PolicyID, tp.StartsAt, now)
			}
		}
		dtos = append(dtos, dto)
	}

	h.jsonResponse(ctx, 200, PaginatedResponse{
		Total: total,
		Page:  page,
		Limit: limit,
		Items: dtos,
	})
}

// UpdateUser updates a user's status, password, and/or plan assignment.
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
	if req.NewPassword != nil && len(*req.NewPassword) < 8 {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "new_password must be at least 8 characters")
		return
	}
	if req.PlanStartsAt != nil && req.PlanExpiresAt != nil && !req.PlanExpiresAt.After(*req.PlanStartsAt) {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "plan_expires_at must be after plan_starts_at")
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
	if req.NewPassword != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "password hash failed")
			return
		}
		updates["password_hash"] = string(hash)
	}
	if len(updates) > 0 {
		updates["updated_at"] = time.Now()
		if err := h.db.Model(&existing).Updates(updates).Error; err != nil {
			h.jsonError(ctx, fasthttp.StatusInternalServerError, "update failed")
			return
		}
	}
	if req.PlanID != nil {
		if err := h.assignUserPlan(ctx, existing.ID, *req.PlanID, req.PlanStartsAt, req.PlanExpiresAt); err != nil {
			h.jsonError(ctx, fasthttp.StatusBadRequest, err.Error())
			return
		}
	}
	if len(updates) == 0 && req.PlanID == nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "no fields to update")
		return
	}
	if err := h.db.Where("id = ? AND deleted_at IS NULL", id).First(&existing).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "reload failed")
		return
	}
	auditUpdateCtx(h.db, "user", id, h.getAdminUser(ctx), ctx, updates)
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
	auditDeleteCtx(h.db, "user", id, h.getAdminUser(ctx), ctx, nil)
	h.jsonResponse(ctx, 200, map[string]interface{}{"deleted": true})
}

func (h *Handler) assignUserPlan(ctx *fasthttp.RequestCtx, userID uuid.UUID, planID uuid.UUID, startsAtReq, expiresAtReq *time.Time) error {
	now := time.Now()
	if planID == uuid.Nil {
		return h.db.Model(&db.TokenPlan{}).
			Where("user_id = ? AND expires_at > ?", userID.String(), now).
			Update("expires_at", now).Error
	}
	var plan db.Plan
	if err := h.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", planID).First(&plan).Error; err != nil {
		return errors.New("plan not found")
	}
	startsAt := now
	if startsAtReq != nil {
		startsAt = *startsAtReq
	}
	expiresAt := startsAt.AddDate(0, 0, plan.DurationDays)
	if expiresAtReq != nil {
		expiresAt = *expiresAtReq
	}
	if !expiresAt.After(startsAt) {
		return errors.New("plan_expires_at must be after plan_starts_at")
	}
	return h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&db.TokenPlan{}).
			Where("user_id = ? AND expires_at > ?", userID.String(), now).
			Update("expires_at", now).Error; err != nil {
			return err
		}
		tokenPlan := db.TokenPlan{
			UserID:    userID.String(),
			PlanID:    plan.ID,
			StartsAt:  startsAt,
			ExpiresAt: expiresAt,
		}
		if err := tx.Create(&tokenPlan).Error; err != nil {
			return err
		}
		return nil
	})
}

func createDefaultUserTokenTx(tx *gorm.DB, userID string) (db.Token, error) {
	keyUUID := uuid.New().String()
	token := db.Token{
		UserID:  userID,
		Name:    "默认密钥",
		Key:     "sk-relay-" + strings.ReplaceAll(keyUUID, "-", ""),
		Enabled: true,
	}
	if err := tx.Create(&token).Error; err != nil {
		return db.Token{}, err
	}
	return token, nil
}

func userDTOFromUser(u db.User) userDTO {
	return userDTO{
		ID:        u.ID.String(),
		Email:     u.Email,
		Username:  u.Username,
		Status:    u.Status,
		CreatedAt: u.CreatedAt.Format(time.RFC3339),
	}
}

func (h *Handler) userUsageWindows(userID string, policyID *uuid.UUID, planStartsAt time.Time, now time.Time) []usageWindowDTO {
	if policyID == nil || *policyID == uuid.Nil {
		return nil
	}
	var policy db.AccessPolicy
	if err := h.db.Where("id = ? AND enabled = true AND deleted_at IS NULL", *policyID).First(&policy).Error; err != nil {
		return nil
	}
	fiveHourStart := h.rollingFiveHourStart(*policyID, userID, now.UTC())
	weekStart := currentWeekFromPlanStart(planStartsAt, now.UTC())
	monthStart := currentMonthFromPlanStart(planStartsAt, now.UTC())
	specs := []struct {
		name  string
		limit int
		start time.Time
		end   time.Time
	}{
		{name: "hour", limit: policy.HourlyLimit, start: fiveHourStart, end: fiveHourStart.Add(5 * time.Hour)},
		{name: "week", limit: policy.WeeklyLimit, start: weekStart, end: weekStart.Add(7 * 24 * time.Hour)},
		{name: "month", limit: policy.MonthlyLimit, start: monthStart, end: monthStart.Add(30 * 24 * time.Hour)},
	}
	windows := make([]usageWindowDTO, 0, len(specs))
	for _, spec := range specs {
		var usage db.PolicyUsageWindow
		used := 0
		if err := h.db.Where("policy_id = ? AND user_id = ? AND window_type = ? AND window_start = ?", *policyID, userID, spec.name, spec.start).First(&usage).Error; err == nil {
			used = usage.UsedCount
		}
		remaining := spec.limit - used
		if remaining < 0 {
			remaining = 0
		}
		windows = append(windows, usageWindowDTO{
			Type:      spec.name,
			Limit:     spec.limit,
			Used:      used,
			Remaining: remaining,
			ResetAt:   spec.end.Format(time.RFC3339),
		})
	}
	return windows
}

func (h *Handler) rollingFiveHourStart(policyID uuid.UUID, userID string, now time.Time) time.Time {
	var usage db.PolicyUsageWindow
	err := h.db.Where("policy_id = ? AND user_id = ? AND window_type = ? AND window_start <= ?", policyID, userID, "hour", now).
		Order("window_start DESC").
		First(&usage).Error
	if err == nil && now.Before(usage.WindowStart.UTC().Add(5*time.Hour)) {
		return usage.WindowStart.UTC()
	}
	return now
}

func currentWeekFromPlanStart(planStartsAt time.Time, now time.Time) time.Time {
	start := planStartsAt.UTC()
	if start.IsZero() || now.Before(start) {
		return now
	}
	elapsed := now.Sub(start)
	return start.Add(time.Duration(int64(elapsed/(7*24*time.Hour))) * 7 * 24 * time.Hour)
}

func currentMonthFromPlanStart(planStartsAt time.Time, now time.Time) time.Time {
	start := planStartsAt.UTC()
	if start.IsZero() || now.Before(start) {
		return now
	}
	elapsed := now.Sub(start)
	return start.Add(time.Duration(int64(elapsed/(30*24*time.Hour))) * 30 * 24 * time.Hour)
}
