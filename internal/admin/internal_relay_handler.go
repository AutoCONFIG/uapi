package admin

import (
	"encoding/json"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/AutoCONFIG/uapi/internal/relay"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RelayConfigResponse struct {
	NodeID   uuid.UUID            `json:"node_id"`
	Version  int64                `json:"version"`
	Channels []db.Channel         `json:"channels"`
	Accounts []RelayConfigAccount `json:"accounts"`
	Bindings []db.NodeAccount     `json:"bindings"`
}

type RelayConfigAccount struct {
	ID            uuid.UUID              `json:"id"`
	ChannelID     uuid.UUID              `json:"channel_id"`
	Name          string                 `json:"name"`
	Credentials   string                 `json:"credentials"`
	CredType      string                 `json:"cred_type"`
	Endpoint      string                 `json:"endpoint"`
	Weight        int                    `json:"weight"`
	Enabled       bool                   `json:"enabled"`
	CooldownUntil *time.Time             `json:"cooldown_until,omitempty"`
	RefreshToken  string                 `json:"refresh_token"`
	TokenExpiry   *time.Time             `json:"token_expiry,omitempty"`
	ClientID      string                 `json:"client_id"`
	ClientSecret  string                 `json:"client_secret"`
	TokenURL      string                 `json:"token_url"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

type UsageEventRequest struct {
	RequestID        string    `json:"request_id"`
	TokenID          uuid.UUID `json:"token_id"`
	TokenPlanID      uuid.UUID `json:"token_plan_id"`
	ChannelID        uuid.UUID `json:"channel_id"`
	AccountID        uuid.UUID `json:"account_id"`
	Model            string    `json:"model"`
	IsStream         bool      `json:"is_stream"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	EstimatedTokens  int       `json:"estimated_tokens"`
	StatusCode       int       `json:"status_code"`
	LatencyMs        int64     `json:"latency_ms"`
}

type RelayAccountUpdateRequest struct {
	AccountID    uuid.UUID              `json:"account_id"`
	ChannelID    uuid.UUID              `json:"channel_id"`
	Credentials  string                 `json:"credentials"`
	RefreshToken string                 `json:"refresh_token"`
	TokenExpiry  *time.Time             `json:"token_expiry,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

func (h *Handler) RelayConfig(ctx *fasthttp.RequestCtx) {
	nodeID, err := uuid.Parse(string(ctx.QueryArgs().Peek("node_id")))
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid node_id")
		return
	}
	var node db.RelayNode
	if err := h.db.Where("id = ? AND deleted_at IS NULL", nodeID).First(&node).Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusNotFound, "node not found")
		return
	}
	var allBindings []db.NodeAccount
	h.db.Where("relay_node_id = ? AND deleted_at IS NULL", nodeID).Find(&allBindings)
	accountIDs := make([]uuid.UUID, 0, len(allBindings))
	for _, b := range allBindings {
		accountIDs = append(accountIDs, b.AccountID)
	}
	var allAccounts []db.Account
	if len(accountIDs) > 0 {
		h.db.Where("id IN ? AND deleted_at IS NULL", accountIDs).Find(&allAccounts)
	}
	var bindings []db.NodeAccount
	for _, b := range allBindings {
		if b.Enabled && b.DeletedAt == nil {
			bindings = append(bindings, b)
		}
	}
	activeAccountIDs := map[uuid.UUID]bool{}
	for _, b := range bindings {
		activeAccountIDs[b.AccountID] = true
	}
	accounts := make([]db.Account, 0, len(allAccounts))
	for _, acc := range allAccounts {
		if activeAccountIDs[acc.ID] && acc.Enabled && acc.DeletedAt == nil {
			accounts = append(accounts, acc)
		}
	}
	runtimeAccounts := make([]RelayConfigAccount, 0, len(accounts))
	for _, acc := range accounts {
		runtimeAccounts = append(runtimeAccounts, RelayConfigAccount{
			ID: acc.ID, ChannelID: acc.ChannelID, Name: acc.Name, Credentials: acc.Credentials,
			CredType: acc.CredType, Endpoint: acc.Endpoint, Weight: acc.Weight, Enabled: acc.Enabled, CooldownUntil: acc.CooldownUntil,
			RefreshToken: acc.RefreshToken, TokenExpiry: acc.TokenExpiry, ClientID: acc.ClientID,
			ClientSecret: acc.ClientSecret, TokenURL: acc.TokenURL, Metadata: acc.Metadata,
		})
	}
	channelSet := map[uuid.UUID]bool{}
	channelIDs := make([]uuid.UUID, 0, len(accounts))
	for _, acc := range accounts {
		if !channelSet[acc.ChannelID] {
			channelSet[acc.ChannelID] = true
			channelIDs = append(channelIDs, acc.ChannelID)
		}
	}
	var allChannels []db.Channel
	if len(channelIDs) > 0 {
		h.db.Where("id IN ? AND deleted_at IS NULL", channelIDs).Find(&allChannels)
	}
	channels := make([]db.Channel, 0, len(allChannels))
	for _, ch := range allChannels {
		if ch.Enabled && ch.DeletedAt == nil {
			channels = append(channels, ch)
		}
	}
	version := node.UpdatedAt.UnixNano()
	version = maxDeletedAtVersion(version, node.DeletedAt)
	for _, b := range allBindings {
		if n := b.UpdatedAt.UnixNano(); n > version {
			version = n
		}
		version = maxDeletedAtVersion(version, b.DeletedAt)
	}
	for _, acc := range allAccounts {
		if n := acc.UpdatedAt.UnixNano(); n > version {
			version = n
		}
		version = maxDeletedAtVersion(version, acc.DeletedAt)
	}
	for _, ch := range allChannels {
		if n := ch.UpdatedAt.UnixNano(); n > version {
			version = n
		}
		version = maxDeletedAtVersion(version, ch.DeletedAt)
	}
	h.jsonResponse(ctx, 200, RelayConfigResponse{NodeID: nodeID, Version: version, Channels: channels, Accounts: runtimeAccounts, Bindings: bindings})
}

func maxDeletedAtVersion(version int64, deletedAt *time.Time) int64 {
	if deletedAt == nil {
		return version
	}
	if n := deletedAt.UnixNano(); n > version {
		return n
	}
	return version
}

func (h *Handler) UsageEvent(ctx *fasthttp.RequestCtx) {
	var req UsageEventRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.RequestID == "" || req.TokenID == uuid.Nil || req.ChannelID == uuid.Nil || req.AccountID == uuid.Nil || req.Model == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "missing required fields")
		return
	}
	if err := h.settleUsageEvent(req); err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "settle failed")
		return
	}
	h.jsonResponse(ctx, 200, map[string]interface{}{"accepted": true})
}

func (h *Handler) RelayAccountUpdate(ctx *fasthttp.RequestCtx) {
	var req RelayAccountUpdateRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "invalid request")
		return
	}
	if req.AccountID == uuid.Nil || req.ChannelID == uuid.Nil || req.Credentials == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "missing required fields")
		return
	}
	updates := map[string]interface{}{
		"credentials": req.Credentials,
	}
	if req.RefreshToken != "" {
		updates["refresh_token"] = req.RefreshToken
	}
	if req.TokenExpiry != nil {
		updates["token_expiry"] = req.TokenExpiry
	}
	if req.Metadata != nil {
		updates["metadata"] = req.Metadata
	}
	q := h.db.Model(&db.Account{}).
		Where("id = ? AND channel_id = ? AND cred_type = ? AND deleted_at IS NULL", req.AccountID, req.ChannelID, "oauth_token")
	if req.TokenExpiry != nil {
		q = q.Where("token_expiry IS NULL OR token_expiry <= ?", *req.TokenExpiry)
	}
	res := q.Updates(updates)
	if err := res.Error; err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "update account failed")
		return
	}
	if res.RowsAffected == 0 {
		h.jsonResponse(ctx, 200, map[string]interface{}{"accepted": false, "reason": "account not found, stale, or not eligible for update"})
		return
	}
	h.jsonResponse(ctx, 200, map[string]interface{}{"accepted": true})
}

func (h *Handler) settleUsageEvent(req UsageEventRequest) error {
	return h.db.Transaction(func(tx *gorm.DB) error {
		event := db.UsageEvent{ID: uuid.New(), RequestID: req.RequestID, TokenID: req.TokenID, TokenPlanID: req.TokenPlanID, ChannelID: req.ChannelID, AccountID: req.AccountID, Model: req.Model, IsStream: req.IsStream, PromptTokens: req.PromptTokens, CompletionTokens: req.CompletionTokens, EstimatedTokens: req.EstimatedTokens, StatusCode: req.StatusCode, LatencyMs: req.LatencyMs, Settled: false, CreatedAt: time.Now()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error; err != nil {
			return err
		}
		var existing db.UsageEvent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", req.RequestID).First(&existing).Error; err != nil {
			return err
		}
		if existing.Settled {
			return nil
		}
		logEntry := db.Log{TokenID: req.TokenID, ChannelID: req.ChannelID, AccountID: req.AccountID, Model: req.Model, IsStream: req.IsStream, PromptTokens: int64(req.PromptTokens), CompletionTokens: int64(req.CompletionTokens), TotalTokens: int64(req.PromptTokens + req.CompletionTokens), LatencyMs: req.LatencyMs, StatusCode: req.StatusCode}
		if err := tx.Create(&logEntry).Error; err != nil {
			return err
		}
		planID := req.TokenPlanID
		if planID == uuid.Nil {
			planID = existing.TokenPlanID
		}
		if err := relay.RefundAndSettleTxForPlan(tx, req.TokenID.String(), planID, req.EstimatedTokens, req.PromptTokens, req.CompletionTokens, 0, 0, req.Model); err != nil {
			return err
		}
		return tx.Model(&existing).Updates(map[string]interface{}{
			"token_plan_id":     planID,
			"prompt_tokens":     req.PromptTokens,
			"completion_tokens": req.CompletionTokens,
			"estimated_tokens":  req.EstimatedTokens,
			"status_code":       req.StatusCode,
			"latency_ms":        req.LatencyMs,
			"is_stream":         req.IsStream,
			"model":             req.Model,
			"settled":           true,
		}).Error
	})
}
