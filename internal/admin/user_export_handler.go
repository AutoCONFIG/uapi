package admin

import (
	"fmt"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

type usersExportSnapshot struct {
	SchemaVersion int                  `yaml:"schema_version"`
	ExportedAt    string               `yaml:"exported_at"`
	Notes         []string             `yaml:"notes,omitempty"`
	Users         []userExport         `yaml:"users"`
	APIKeys       []apiKeyExport       `yaml:"api_keys,omitempty"`
	Subscriptions []subscriptionExport `yaml:"subscriptions,omitempty"`
	QuotaWindows  []quotaWindowExport  `yaml:"quota_windows,omitempty"`
}

type userExport struct {
	ID           string    `yaml:"id"`
	Email        string    `yaml:"email"`
	Username     string    `yaml:"username"`
	PasswordHash string    `yaml:"password_hash"`
	Status       string    `yaml:"status"`
	CreatedAt    time.Time `yaml:"created_at"`
	UpdatedAt    time.Time `yaml:"updated_at"`
}

type apiKeyExport struct {
	ID          string     `yaml:"id"`
	UserID      string     `yaml:"user_id"`
	Username    string     `yaml:"username,omitempty"`
	Name        string     `yaml:"name"`
	Key         string     `yaml:"key"`
	Enabled     bool       `yaml:"enabled"`
	IPWhitelist string     `yaml:"ip_whitelist,omitempty"`
	ExpiresAt   *time.Time `yaml:"expires_at,omitempty"`
	Models      string     `yaml:"models,omitempty"`
	Permissions string     `yaml:"permissions,omitempty"`
	CreatedAt   time.Time  `yaml:"created_at"`
	UpdatedAt   time.Time  `yaml:"updated_at"`
}

type subscriptionExport struct {
	ID        string    `yaml:"id"`
	UserID    string    `yaml:"user_id"`
	Username  string    `yaml:"username,omitempty"`
	PlanID    string    `yaml:"plan_id"`
	PlanName  string    `yaml:"plan_name,omitempty"`
	PlanType  string    `yaml:"plan_type,omitempty"`
	StartsAt  time.Time `yaml:"starts_at"`
	ExpiresAt time.Time `yaml:"expires_at"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
}

type quotaWindowExport struct {
	ID          string    `yaml:"id"`
	PolicyID    string    `yaml:"policy_id"`
	UserID      string    `yaml:"user_id"`
	Username    string    `yaml:"username,omitempty"`
	WindowType  string    `yaml:"window_type"`
	WindowStart time.Time `yaml:"window_start"`
	UsedCount   int       `yaml:"used_count"`
	Limit       int       `yaml:"limit"`
	Remaining   int       `yaml:"remaining"`
	ResetAt     time.Time `yaml:"reset_at"`
	CreatedAt   time.Time `yaml:"created_at"`
	UpdatedAt   time.Time `yaml:"updated_at"`
}

func (h *Handler) HandleUsersExport(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "POST" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.verifyExportPassword(ctx) {
		return
	}
	snapshot, err := h.buildUsersExportSnapshot()
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "export users failed")
		return
	}
	data, err := yaml.Marshal(snapshot)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "encode users failed")
		return
	}
	filename := "uapi-users-" + time.Now().UTC().Format("20060102-150405") + ".yaml"
	ctx.Response.Header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetContentType("application/x-yaml; charset=utf-8")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(data)
	createAuditLogWithValues(h.db, "export", "users", "runtime", h.getAdminUser(ctx), auditIP(ctx), "", auditJSON(map[string]interface{}{"format": "yaml"}))
}

func (h *Handler) buildUsersExportSnapshot() (usersExportSnapshot, error) {
	snapshot := usersExportSnapshot{
		SchemaVersion: 1,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		Notes: []string{
			"This snapshot contains user password hashes and API keys. Keep it private.",
			"Usage logs and request event history are intentionally excluded.",
		},
	}

	var users []db.User
	if err := h.db.Where("deleted_at IS NULL").Order("created_at asc").Find(&users).Error; err != nil {
		return snapshot, err
	}
	usernames := make(map[string]string, len(users))
	for _, user := range users {
		userID := user.ID.String()
		usernames[userID] = user.Username
		snapshot.Users = append(snapshot.Users, userExport{
			ID:           userID,
			Email:        user.Email,
			Username:     user.Username,
			PasswordHash: user.PasswordHash,
			Status:       user.Status,
			CreatedAt:    user.CreatedAt,
			UpdatedAt:    user.UpdatedAt,
		})
	}

	var tokens []db.Token
	if err := h.db.Where("deleted_at IS NULL").Order("created_at asc").Find(&tokens).Error; err != nil {
		return snapshot, err
	}
	for _, token := range tokens {
		snapshot.APIKeys = append(snapshot.APIKeys, apiKeyExport{
			ID:          token.ID.String(),
			UserID:      token.UserID,
			Username:    usernames[token.UserID],
			Name:        token.Name,
			Key:         token.Key,
			Enabled:     token.Enabled,
			IPWhitelist: token.IPWhitelist,
			ExpiresAt:   token.ExpiresAt,
			Models:      token.Models,
			Permissions: token.Permissions,
			CreatedAt:   token.CreatedAt,
			UpdatedAt:   token.UpdatedAt,
		})
	}

	plans, err := h.exportPlanIndex()
	if err != nil {
		return snapshot, err
	}
	now := time.Now()
	var tokenPlans []db.TokenPlan
	if err := h.db.Where("deleted_at IS NULL AND expires_at > ?", now).Order("created_at asc").Find(&tokenPlans).Error; err != nil {
		return snapshot, err
	}
	for _, tokenPlan := range tokenPlans {
		plan := plans[tokenPlan.PlanID]
		snapshot.Subscriptions = append(snapshot.Subscriptions, subscriptionExport{
			ID:        tokenPlan.ID.String(),
			UserID:    tokenPlan.UserID,
			Username:  usernames[tokenPlan.UserID],
			PlanID:    tokenPlan.PlanID.String(),
			PlanName:  plan.Name,
			PlanType:  plan.Type,
			StartsAt:  tokenPlan.StartsAt,
			ExpiresAt: tokenPlan.ExpiresAt,
			CreatedAt: tokenPlan.CreatedAt,
			UpdatedAt: tokenPlan.UpdatedAt,
		})
	}

	policies, err := h.exportPolicyIndex()
	if err != nil {
		return snapshot, err
	}
	var windows []db.PolicyUsageWindow
	if err := h.db.Order("created_at asc").Find(&windows).Error; err != nil {
		return snapshot, err
	}
	for _, window := range windows {
		limit := quotaLimitForWindow(policies[window.PolicyID], window.WindowType)
		remaining := limit - window.UsedCount
		if remaining < 0 {
			remaining = 0
		}
		snapshot.QuotaWindows = append(snapshot.QuotaWindows, quotaWindowExport{
			ID:          window.ID.String(),
			PolicyID:    window.PolicyID.String(),
			UserID:      window.UserID,
			Username:    usernames[window.UserID],
			WindowType:  window.WindowType,
			WindowStart: window.WindowStart,
			UsedCount:   window.UsedCount,
			Limit:       limit,
			Remaining:   remaining,
			ResetAt:     quotaWindowResetAt(window.WindowType, window.WindowStart),
			CreatedAt:   window.CreatedAt,
			UpdatedAt:   window.UpdatedAt,
		})
	}

	return snapshot, nil
}

func (h *Handler) exportPlanIndex() (map[uuid.UUID]db.Plan, error) {
	var plans []db.Plan
	if err := h.db.Where("deleted_at IS NULL").Find(&plans).Error; err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]db.Plan, len(plans))
	for _, plan := range plans {
		out[plan.ID] = plan
	}
	return out, nil
}

func (h *Handler) exportPolicyIndex() (map[uuid.UUID]db.AccessPolicy, error) {
	var policies []db.AccessPolicy
	if err := h.db.Where("deleted_at IS NULL").Find(&policies).Error; err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]db.AccessPolicy, len(policies))
	for _, policy := range policies {
		out[policy.ID] = policy
	}
	return out, nil
}

func quotaLimitForWindow(policy db.AccessPolicy, windowType string) int {
	switch windowType {
	case "hour":
		return policy.HourlyLimit
	case "week":
		return policy.WeeklyLimit
	case "month":
		return policy.MonthlyLimit
	default:
		return 0
	}
}

func quotaWindowResetAt(windowType string, start time.Time) time.Time {
	switch windowType {
	case "hour":
		return start.Add(5 * time.Hour)
	case "week":
		return start.AddDate(0, 0, 7)
	case "month":
		return start.Add(30 * 24 * time.Hour)
	default:
		return start
	}
}
