package admin

import (
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

type AdminUsageLogItem struct {
	ID               int64     `json:"id"`
	CreatedAt        time.Time `json:"created_at"`
	TokenID          uuid.UUID `json:"token_id"`
	UserID           string    `json:"user_id,omitempty"`
	Username         string    `json:"username,omitempty"`
	UserEmail        string    `json:"user_email,omitempty"`
	ClientIP         string    `json:"client_ip,omitempty"`
	ChannelID        uuid.UUID `json:"channel_id"`
	ChannelName      string    `json:"channel_name,omitempty"`
	AccountID        uuid.UUID `json:"account_id"`
	AccountName      string    `json:"account_name,omitempty"`
	AccountCredType  string    `json:"account_cred_type,omitempty"`
	Model            string    `json:"model"`
	IsStream         bool      `json:"is_stream"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
	LatencyMs        int64     `json:"latency_ms"`
	StatusCode       int       `json:"status_code"`
	ErrorMessage     string    `json:"error_message,omitempty"`
}

// HandleLogs returns a paginated list of request logs.
func (h *Handler) HandleLogs(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "GET" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	page, limit := h.parsePagination(ctx)
	offset := (page - 1) * limit
	query := h.db.Table("logs").
		Select("logs.id, logs.created_at, logs.token_id, tokens.user_id, users.username, users.email AS user_email, logs.client_ip, logs.channel_id, channels.name AS channel_name, logs.account_id, accounts.name AS account_name, accounts.cred_type AS account_cred_type, logs.model, logs.is_stream, logs.prompt_tokens, logs.completion_tokens, logs.total_tokens, logs.latency_ms, logs.status_code, logs.error_message").
		Joins("LEFT JOIN tokens ON tokens.id = logs.token_id").
		Joins("LEFT JOIN users ON users.id::text = tokens.user_id").
		Joins("LEFT JOIN channels ON channels.id = logs.channel_id").
		Joins("LEFT JOIN accounts ON accounts.id = logs.account_id")
	if userQuery := strings.TrimSpace(string(ctx.QueryArgs().Peek("user"))); userQuery != "" {
		like := "%" + userQuery + "%"
		query = query.Where("tokens.user_id ILIKE ? OR users.username ILIKE ? OR users.email ILIKE ?", like, like, like)
	}
	if ip := strings.TrimSpace(string(ctx.QueryArgs().Peek("ip"))); ip != "" {
		query = query.Where("logs.client_ip ILIKE ?", "%"+ip+"%")
	}
	if model := strings.TrimSpace(string(ctx.QueryArgs().Peek("model"))); model != "" {
		query = query.Where("logs.model ILIKE ?", "%"+model+"%")
	}
	if startRaw := strings.TrimSpace(string(ctx.QueryArgs().Peek("start"))); startRaw != "" {
		if start, err := time.Parse(time.RFC3339, startRaw); err == nil {
			query = query.Where("logs.created_at >= ?", start)
		}
	}
	if endRaw := strings.TrimSpace(string(ctx.QueryArgs().Peek("end"))); endRaw != "" {
		if end, err := time.Parse(time.RFC3339, endRaw); err == nil {
			query = query.Where("logs.created_at <= ?", end)
		}
	}
	var total int64
	var items []AdminUsageLogItem
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "failed to count logs")
		return
	}
	if err := query.Order("logs.created_at desc").Limit(limit).Offset(offset).Scan(&items).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "failed to query logs")
		return
	}
	if items == nil {
		items = []AdminUsageLogItem{}
	}
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
