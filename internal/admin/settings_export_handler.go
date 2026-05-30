package admin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/AutoCONFIG/uapi/internal/appsettings"
	internalcrypto "github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

type settingsExportSnapshot struct {
	SchemaVersion   int                   `yaml:"schema_version"`
	ExportedAt      string                `yaml:"exported_at"`
	Notes           []string              `yaml:"notes,omitempty"`
	RuntimeSettings runtimeSettingsExport `yaml:"runtime_settings"`
	Channels        []channelExport       `yaml:"channels"`
	Accounts        []accountExport       `yaml:"accounts,omitempty"`
	Plans           []planExport          `yaml:"plans"`
	AccessPolicies  []accessPolicyExport  `yaml:"access_policies,omitempty"`
	RelayNodes      []relayNodeExport     `yaml:"relay_nodes,omitempty"`
	NodeChannels    []nodeChannelExport   `yaml:"node_channels,omitempty"`
	SystemSettings  []systemSettingExport `yaml:"system_settings"`
}

type runtimeSettingsExport struct {
	AdminUsername           string      `yaml:"admin_username"`
	LogRetentionDays        int         `yaml:"log_retention_days"`
	RedeemCodeRetentionDays int         `yaml:"redeem_code_retention_days"`
	MaxKeysPerUser          int         `yaml:"max_keys_per_user"`
	AdminPasswordHash       string      `yaml:"admin_password_hash,omitempty"`
	ModelRatios             interface{} `yaml:"model_ratios"`
	Background              string      `yaml:"background"`
	PublicBaseURL           string      `yaml:"public_base_url,omitempty"`
}

type channelExport struct {
	ID           string      `yaml:"id"`
	Name         string      `yaml:"name"`
	Type         string      `yaml:"type"`
	Group        string      `yaml:"group"`
	Endpoint     string      `yaml:"endpoint"`
	Enabled      bool        `yaml:"enabled"`
	Models       string      `yaml:"models,omitempty"`
	ModelAliases string      `yaml:"model_aliases,omitempty"`
	Priority     int         `yaml:"priority"`
	APIFormat    string      `yaml:"api_format"`
	ForceStream  bool        `yaml:"force_stream"`
	AffinityTTL  int         `yaml:"affinity_ttl"`
	Settings     interface{} `yaml:"settings"`
}

type accountExport struct {
	ID                 string                 `yaml:"id"`
	ChannelID          string                 `yaml:"channel_id"`
	ChannelName        string                 `yaml:"channel_name,omitempty"`
	Name               string                 `yaml:"name"`
	CredType           string                 `yaml:"cred_type"`
	Credential         string                 `yaml:"credential"`
	StoredCredential   string                 `yaml:"stored_credential"`
	Endpoint           string                 `yaml:"endpoint,omitempty"`
	Weight             int                    `yaml:"weight"`
	Enabled            bool                   `yaml:"enabled"`
	RefreshToken       string                 `yaml:"refresh_token,omitempty"`
	StoredRefreshToken string                 `yaml:"stored_refresh_token,omitempty"`
	TokenExpiry        *time.Time             `yaml:"token_expiry,omitempty"`
	ClientID           string                 `yaml:"client_id,omitempty"`
	ClientSecret       string                 `yaml:"client_secret,omitempty"`
	StoredClientSecret string                 `yaml:"stored_client_secret,omitempty"`
	TokenURL           string                 `yaml:"token_url,omitempty"`
	Metadata           map[string]interface{} `yaml:"metadata,omitempty"`
}

type planExport struct {
	ID           string              `yaml:"id"`
	Name         string              `yaml:"name"`
	Type         string              `yaml:"type"`
	Enabled      bool                `yaml:"enabled"`
	Public       bool                `yaml:"public"`
	DurationDays int                 `yaml:"duration_days"`
	PolicyID     string              `yaml:"policy_id,omitempty"`
	Policy       *accessPolicyExport `yaml:"policy,omitempty"`
}

type accessPolicyExport struct {
	ID             string `yaml:"id"`
	AllowedModels  string `yaml:"allowed_models,omitempty"`
	MaxConcurrency int    `yaml:"max_concurrency"`
	HourlyLimit    int    `yaml:"hourly_limit"`
	WeeklyLimit    int    `yaml:"weekly_limit"`
	MonthlyLimit   int    `yaml:"monthly_limit"`
	Enabled        bool   `yaml:"enabled"`
}

type relayNodeExport struct {
	ID             string `yaml:"id"`
	Name           string `yaml:"name"`
	BaseURL        string `yaml:"base_url"`
	Region         string `yaml:"region,omitempty"`
	EgressIP       string `yaml:"egress_ip,omitempty"`
	Weight         int    `yaml:"weight"`
	MaxConcurrency int    `yaml:"max_concurrency"`
	Status         string `yaml:"status"`
}

type nodeChannelExport struct {
	ID          string `yaml:"id"`
	RelayNodeID string `yaml:"relay_node_id"`
	ChannelID   string `yaml:"channel_id"`
	Weight      int    `yaml:"weight"`
	Enabled     bool   `yaml:"enabled"`
}

type systemSettingExport struct {
	Key   string      `yaml:"key"`
	Value interface{} `yaml:"value"`
}

func (h *Handler) HandleSettingsExport(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "POST" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.verifyExportPassword(ctx) {
		return
	}
	snapshot, err := h.buildSettingsExportSnapshot()
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "export settings failed")
		return
	}
	data, err := yaml.Marshal(snapshot)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "encode settings failed")
		return
	}
	filename := "uapi-settings-" + time.Now().UTC().Format("20060102-150405") + ".yaml"
	ctx.Response.Header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetContentType("application/x-yaml; charset=utf-8")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(data)
	createAuditLogWithValues(h.db, "export", "settings", "runtime", h.getAdminUser(ctx), auditIP(ctx), "", auditJSON(map[string]interface{}{"format": "yaml"}))
}

func (h *Handler) verifyExportPassword(ctx *fasthttp.RequestCtx) bool {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil || strings.TrimSpace(req.Password) == "" {
		h.jsonError(ctx, fasthttp.StatusBadRequest, "password is required")
		return false
	}
	_, adminPasswordHash := h.adminCredentials()
	if adminPasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(adminPasswordHash), []byte(req.Password)) != nil {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "invalid password")
		return false
	}
	return true
}

func (h *Handler) buildSettingsExportSnapshot() (settingsExportSnapshot, error) {
	settings := h.settingsResponse()
	snapshot := settingsExportSnapshot{
		SchemaVersion: 1,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		Notes: []string{
			"This snapshot contains sensitive restore material, including plaintext account credentials, OAuth tokens, client secrets, encrypted database credential values, and password hashes.",
			"Keep this YAML private. It is intended for minimal operational restore, not public diagnostics.",
		},
		RuntimeSettings: runtimeSettingsExport{
			AdminUsername:           settings.AdminUsername,
			LogRetentionDays:        settings.LogRetentionDays,
			RedeemCodeRetentionDays: settings.RedeemCodeRetentionDays,
			MaxKeysPerUser:          settings.MaxKeysPerUser,
			AdminPasswordHash:       appsettings.Get(h.db, appsettings.AdminPasswordHash, ""),
			ModelRatios:             parseJSONSetting(settings.ModelRatios),
			Background:              settings.Background,
			PublicBaseURL:           settings.PublicBaseURL,
		},
	}

	var channels []db.Channel
	if err := h.db.Where("deleted_at IS NULL").Order("created_at asc").Find(&channels).Error; err != nil {
		return snapshot, err
	}
	channelNames := make(map[uuid.UUID]string, len(channels))
	snapshot.Channels = make([]channelExport, 0, len(channels))
	for _, ch := range channels {
		channelNames[ch.ID] = ch.Name
		snapshot.Channels = append(snapshot.Channels, channelExport{
			ID:           ch.ID.String(),
			Name:         ch.Name,
			Type:         ch.Type,
			Group:        ch.Group,
			Endpoint:     ch.Endpoint,
			Enabled:      ch.Enabled,
			Models:       ch.Models,
			ModelAliases: ch.ModelAliases,
			Priority:     ch.Priority,
			APIFormat:    ch.APIFormat,
			ForceStream:  ch.ForceStream,
			AffinityTTL:  ch.AffinityTTL,
			Settings:     parseJSONSetting(ch.Settings),
		})
	}

	var accounts []db.Account
	if err := h.db.Where("deleted_at IS NULL").Order("created_at asc").Find(&accounts).Error; err != nil {
		return snapshot, err
	}
	snapshot.Accounts = make([]accountExport, 0, len(accounts))
	for _, acc := range accounts {
		credential, err := internalcrypto.Decrypt(acc.Credentials)
		if err != nil {
			return snapshot, err
		}
		refreshToken := ""
		if acc.RefreshToken != "" {
			refreshToken, err = internalcrypto.Decrypt(acc.RefreshToken)
			if err != nil {
				return snapshot, err
			}
		}
		clientSecret := ""
		if acc.ClientSecret != "" {
			clientSecret, err = internalcrypto.Decrypt(acc.ClientSecret)
			if err != nil {
				return snapshot, err
			}
		}
		snapshot.Accounts = append(snapshot.Accounts, accountExport{
			ID:                 acc.ID.String(),
			ChannelID:          acc.ChannelID.String(),
			ChannelName:        channelNames[acc.ChannelID],
			Name:               acc.Name,
			CredType:           acc.CredType,
			Credential:         credential,
			StoredCredential:   acc.Credentials,
			Endpoint:           acc.Endpoint,
			Weight:             acc.Weight,
			Enabled:            acc.Enabled,
			RefreshToken:       refreshToken,
			StoredRefreshToken: acc.RefreshToken,
			TokenExpiry:        acc.TokenExpiry,
			ClientID:           acc.ClientID,
			ClientSecret:       clientSecret,
			StoredClientSecret: acc.ClientSecret,
			TokenURL:           acc.TokenURL,
			Metadata:           acc.Metadata,
		})
	}

	policyMap, err := h.exportAccessPolicies()
	if err != nil {
		return snapshot, err
	}
	snapshot.AccessPolicies = make([]accessPolicyExport, 0, len(policyMap))
	policyIDs := make([]string, 0, len(policyMap))
	for id := range policyMap {
		policyIDs = append(policyIDs, id.String())
	}
	sort.Strings(policyIDs)
	for _, id := range policyIDs {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return snapshot, err
		}
		snapshot.AccessPolicies = append(snapshot.AccessPolicies, policyMap[parsed])
	}

	var plans []db.Plan
	if err := h.db.Where("deleted_at IS NULL").Order("created_at asc").Find(&plans).Error; err != nil {
		return snapshot, err
	}
	snapshot.Plans = make([]planExport, 0, len(plans))
	for _, plan := range plans {
		item := planExport{
			ID:           plan.ID.String(),
			Name:         plan.Name,
			Type:         plan.Type,
			Enabled:      plan.Enabled,
			Public:       plan.Public,
			DurationDays: plan.DurationDays,
		}
		if plan.PolicyID != nil {
			item.PolicyID = plan.PolicyID.String()
			if policy, ok := policyMap[*plan.PolicyID]; ok {
				policyCopy := policy
				item.Policy = &policyCopy
			}
		}
		snapshot.Plans = append(snapshot.Plans, item)
	}

	var relayNodes []db.RelayNode
	if err := h.db.Where("deleted_at IS NULL").Order("created_at asc").Find(&relayNodes).Error; err != nil {
		return snapshot, err
	}
	snapshot.RelayNodes = make([]relayNodeExport, 0, len(relayNodes))
	for _, node := range relayNodes {
		snapshot.RelayNodes = append(snapshot.RelayNodes, relayNodeExport{
			ID:             node.ID.String(),
			Name:           node.Name,
			BaseURL:        node.BaseURL,
			Region:         node.Region,
			EgressIP:       node.EgressIP,
			Weight:         node.Weight,
			MaxConcurrency: node.MaxConcurrency,
			Status:         node.Status,
		})
	}

	var nodeChannels []db.NodeChannel
	if err := h.db.Where("deleted_at IS NULL").Order("created_at asc").Find(&nodeChannels).Error; err != nil {
		return snapshot, err
	}
	snapshot.NodeChannels = make([]nodeChannelExport, 0, len(nodeChannels))
	for _, item := range nodeChannels {
		snapshot.NodeChannels = append(snapshot.NodeChannels, nodeChannelExport{
			ID:          item.ID.String(),
			RelayNodeID: item.RelayNodeID.String(),
			ChannelID:   item.ChannelID.String(),
			Weight:      item.Weight,
			Enabled:     item.Enabled,
		})
	}

	var systemSettings []db.SystemSetting
	if err := h.db.Order("key asc").Find(&systemSettings).Error; err != nil {
		return snapshot, err
	}
	for _, setting := range systemSettings {
		if !exportableSystemSetting(setting.Key) {
			continue
		}
		snapshot.SystemSettings = append(snapshot.SystemSettings, systemSettingExport{
			Key:   setting.Key,
			Value: parseJSONSetting(setting.Value),
		})
	}

	return snapshot, nil
}

func (h *Handler) exportAccessPolicies() (map[uuid.UUID]accessPolicyExport, error) {
	var policies []db.AccessPolicy
	if err := h.db.Where("deleted_at IS NULL").Order("created_at asc").Find(&policies).Error; err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]accessPolicyExport, len(policies))
	for _, policy := range policies {
		out[policy.ID] = accessPolicyExport{
			ID:             policy.ID.String(),
			AllowedModels:  policy.AllowedModels,
			MaxConcurrency: policy.MaxConcurrency,
			HourlyLimit:    policy.HourlyLimit,
			WeeklyLimit:    policy.WeeklyLimit,
			MonthlyLimit:   policy.MonthlyLimit,
			Enabled:        policy.Enabled,
		}
	}
	return out, nil
}

func exportableSystemSetting(key string) bool {
	switch key {
	case appsettings.UIWallpaperPath:
		return false
	}
	return true
}

func parseJSONSetting(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return raw
	}
	return decoded
}
