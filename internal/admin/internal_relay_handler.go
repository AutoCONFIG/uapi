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
	ID            uuid.UUID  `json:"id"`
	ChannelID     uuid.UUID  `json:"channel_id"`
	Name          string     `json:"name"`
	Credentials   string     `json:"credentials"`
	CredType      string     `json:"cred_type"`
	Weight        int        `json:"weight"`
	Enabled       bool       `json:"enabled"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	RefreshToken  string     `json:"refresh_token"`
	TokenExpiry   *time.Time `json:"token_expiry,omitempty"`
	ClientID      string     `json:"client_id"`
	ClientSecret  string     `json:"client_secret"`
	TokenURL      string     `json:"token_url"`
}

type UsageEventRequest struct {
	RequestID        string    `json:"request_id"`
	TokenID          uuid.UUID `json:"token_id"`
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
	var bindings []db.NodeAccount
	h.db.Where("relay_node_id = ? AND enabled = true AND deleted_at IS NULL", nodeID).Find(&bindings)
	accountIDs := make([]uuid.UUID, 0, len(bindings))
	for _, b := range bindings {
		accountIDs = append(accountIDs, b.AccountID)
	}
	var accounts []db.Account
	if len(accountIDs) > 0 {
		h.db.Where("id IN ? AND enabled = true AND deleted_at IS NULL", accountIDs).Find(&accounts)
	}
	runtimeAccounts := make([]RelayConfigAccount, 0, len(accounts))
	for _, acc := range accounts {
		runtimeAccounts = append(runtimeAccounts, RelayConfigAccount{
			ID: acc.ID, ChannelID: acc.ChannelID, Name: acc.Name, Credentials: acc.Credentials,
			CredType: acc.CredType, Weight: acc.Weight, Enabled: acc.Enabled, CooldownUntil: acc.CooldownUntil,
			RefreshToken: acc.RefreshToken, TokenExpiry: acc.TokenExpiry, ClientID: acc.ClientID,
			ClientSecret: acc.ClientSecret, TokenURL: acc.TokenURL,
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
	var channels []db.Channel
	if len(channelIDs) > 0 {
		h.db.Where("id IN ? AND enabled = true AND deleted_at IS NULL", channelIDs).Find(&channels)
	}
	version := node.UpdatedAt.UnixNano()
	for _, b := range bindings {
		if n := b.UpdatedAt.UnixNano(); n > version {
			version = n
		}
	}
	for _, acc := range accounts {
		if n := acc.UpdatedAt.UnixNano(); n > version {
			version = n
		}
	}
	for _, ch := range channels {
		if n := ch.UpdatedAt.UnixNano(); n > version {
			version = n
		}
	}
	h.jsonResponse(ctx, 200, RelayConfigResponse{NodeID: nodeID, Version: version, Channels: channels, Accounts: runtimeAccounts, Bindings: bindings})
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

func (h *Handler) settleUsageEvent(req UsageEventRequest) error {
	return h.db.Transaction(func(tx *gorm.DB) error {
		event := db.UsageEvent{ID: uuid.New(), RequestID: req.RequestID, TokenID: req.TokenID, ChannelID: req.ChannelID, AccountID: req.AccountID, Model: req.Model, IsStream: req.IsStream, PromptTokens: req.PromptTokens, CompletionTokens: req.CompletionTokens, EstimatedTokens: req.EstimatedTokens, StatusCode: req.StatusCode, LatencyMs: req.LatencyMs, Settled: false, CreatedAt: time.Now()}
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
		if err := relay.RefundAndSettleTx(tx, req.TokenID.String(), req.EstimatedTokens, req.PromptTokens, req.CompletionTokens, req.Model); err != nil {
			return err
		}
		return tx.Model(&existing).Updates(map[string]interface{}{"settled": true}).Error
	})
}
