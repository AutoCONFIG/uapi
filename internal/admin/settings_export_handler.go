package admin

import (
	"encoding/json"
	"fmt"
	"io"
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
	"gorm.io/gorm"
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
	if !h.verifyAdminPasswordValue(req.Password) {
		h.jsonError(ctx, fasthttp.StatusUnauthorized, "invalid password")
		return false
	}
	return true
}

func (h *Handler) verifyAdminPasswordValue(password string) bool {
	_, adminPasswordHash := h.adminCredentials()
	return adminPasswordHash != "" && bcrypt.CompareHashAndPassword([]byte(adminPasswordHash), []byte(password)) == nil
}

func (h *Handler) HandleSettingsImport(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != "POST" {
		h.jsonError(ctx, fasthttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	snapshot, err := h.parseSettingsImport(ctx)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	result, err := h.restoreSettingsSnapshot(snapshot)
	if err != nil {
		h.jsonError(ctx, fasthttp.StatusInternalServerError, "restore settings failed: "+err.Error())
		return
	}
	createAuditLogWithValues(h.db, "import", "settings", "runtime", h.getAdminUser(ctx), auditIP(ctx), "", auditJSON(result))
	h.jsonResponse(ctx, 200, result)
}

func (h *Handler) parseSettingsImport(ctx *fasthttp.RequestCtx) (settingsExportSnapshot, error) {
	var snapshot settingsExportSnapshot
	form, err := ctx.MultipartForm()
	if err != nil {
		return snapshot, fmt.Errorf("invalid multipart form")
	}
	password := ""
	if values := form.Value["password"]; len(values) > 0 {
		password = values[0]
	}
	if strings.TrimSpace(password) == "" {
		return snapshot, fmt.Errorf("password is required")
	}
	if !h.verifyAdminPasswordValue(password) {
		return snapshot, fmt.Errorf("invalid password")
	}
	files := form.File["file"]
	if len(files) == 0 {
		return snapshot, fmt.Errorf("file is required")
	}
	file := files[0]
	if file.Size > 16*1024*1024 {
		return snapshot, fmt.Errorf("file must be 16MB or smaller")
	}
	opened, err := file.Open()
	if err != nil {
		return snapshot, fmt.Errorf("open file failed")
	}
	defer opened.Close()
	data, err := io.ReadAll(opened)
	if err != nil {
		return snapshot, fmt.Errorf("read file failed")
	}
	if err := yaml.Unmarshal(data, &snapshot); err != nil {
		return snapshot, fmt.Errorf("invalid yaml")
	}
	if snapshot.SchemaVersion <= 0 {
		return snapshot, fmt.Errorf("unsupported settings snapshot")
	}
	return snapshot, nil
}

func (h *Handler) restoreSettingsSnapshot(snapshot settingsExportSnapshot) (map[string]int, error) {
	result := map[string]int{}
	refreshedChannels := map[string]struct{}{}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		settings := map[string]string{
			appsettings.AdminUsername:           snapshot.RuntimeSettings.AdminUsername,
			appsettings.LogRetentionDays:        intString(snapshot.RuntimeSettings.LogRetentionDays),
			appsettings.RedeemCodeRetentionDays: intString(snapshot.RuntimeSettings.RedeemCodeRetentionDays),
			appsettings.UserMaxKeysPerUser:      intString(snapshot.RuntimeSettings.MaxKeysPerUser),
			appsettings.ModelRatios:             yamlValueString(snapshot.RuntimeSettings.ModelRatios),
			appsettings.UIBackground:            snapshot.RuntimeSettings.Background,
			appsettings.UIPublicBaseURL:         snapshot.RuntimeSettings.PublicBaseURL,
		}
		if snapshot.RuntimeSettings.AdminPasswordHash != "" {
			settings[appsettings.AdminPasswordHash] = snapshot.RuntimeSettings.AdminPasswordHash
		}
		for _, item := range snapshot.SystemSettings {
			if exportableSystemSetting(item.Key) && item.Key != "" {
				settings[item.Key] = yamlValueString(item.Value)
			}
		}
		for key, value := range settings {
			if key == "" {
				continue
			}
			if err := appsettings.Set(tx, key, value); err != nil {
				return err
			}
			result["system_settings"]++
		}

		for _, item := range snapshot.AccessPolicies {
			id, err := uuid.Parse(item.ID)
			if err != nil {
				return err
			}
			policy := db.AccessPolicy{
				AllowedModels:  item.AllowedModels,
				MaxConcurrency: item.MaxConcurrency,
				HourlyLimit:    item.HourlyLimit,
				WeeklyLimit:    item.WeeklyLimit,
				MonthlyLimit:   item.MonthlyLimit,
				Enabled:        item.Enabled,
			}
			policy.ID = id
			if err := saveBaseModel(tx, &policy); err != nil {
				return err
			}
			result["access_policies"]++
		}

		for _, item := range snapshot.Channels {
			id, err := uuid.Parse(item.ID)
			if err != nil {
				return err
			}
			ch := db.Channel{
				Name:         item.Name,
				Type:         item.Type,
				Group:        normalizeChannelGroup(item.Group),
				Endpoint:     item.Endpoint,
				Enabled:      item.Enabled,
				Models:       item.Models,
				ModelAliases: item.ModelAliases,
				Priority:     item.Priority,
				APIFormat:    item.APIFormat,
				ForceStream:  item.ForceStream,
				AffinityTTL:  item.AffinityTTL,
				Settings:     normalizeChannelSettings(yamlValueString(item.Settings)),
			}
			ch.ID = id
			if err := saveBaseModel(tx, &ch); err != nil {
				return err
			}
			refreshedChannels[id.String()] = struct{}{}
			result["channels"]++
		}

		for _, item := range snapshot.Plans {
			id, err := uuid.Parse(item.ID)
			if err != nil {
				return err
			}
			var policyID *uuid.UUID
			if item.PolicyID != "" {
				parsed, err := uuid.Parse(item.PolicyID)
				if err != nil {
					return err
				}
				policyID = &parsed
			}
			plan := db.Plan{
				Name:         item.Name,
				Type:         item.Type,
				PolicyID:     policyID,
				Enabled:      item.Enabled,
				Public:       item.Public,
				DurationDays: item.DurationDays,
			}
			plan.ID = id
			if err := saveBaseModel(tx, &plan); err != nil {
				return err
			}
			result["plans"]++
		}

		for _, item := range snapshot.Accounts {
			id, err := uuid.Parse(item.ID)
			if err != nil {
				return err
			}
			channelID, err := uuid.Parse(item.ChannelID)
			if err != nil {
				return err
			}
			credential, err := restoreEncryptedValue(item.Credential, item.StoredCredential)
			if err != nil {
				return err
			}
			refreshToken, err := restoreOptionalEncryptedValue(item.RefreshToken, item.StoredRefreshToken)
			if err != nil {
				return err
			}
			clientSecret, err := restoreOptionalEncryptedValue(item.ClientSecret, item.StoredClientSecret)
			if err != nil {
				return err
			}
			account := db.Account{
				ChannelID:    channelID,
				Name:         item.Name,
				Credentials:  credential,
				CredType:     item.CredType,
				Endpoint:     item.Endpoint,
				Weight:       item.Weight,
				Enabled:      item.Enabled,
				RefreshToken: refreshToken,
				TokenExpiry:  item.TokenExpiry,
				ClientID:     item.ClientID,
				ClientSecret: clientSecret,
				TokenURL:     item.TokenURL,
				Metadata:     item.Metadata,
			}
			account.ID = id
			if account.CredType == "" {
				account.CredType = "api_key"
			}
			if account.Weight == 0 {
				account.Weight = 1
			}
			if err := saveBaseModel(tx, &account); err != nil {
				return err
			}
			refreshedChannels[channelID.String()] = struct{}{}
			result["accounts"]++
		}

		for _, item := range snapshot.RelayNodes {
			id, err := uuid.Parse(item.ID)
			if err != nil {
				return err
			}
			node := db.RelayNode{
				Name:           item.Name,
				BaseURL:        item.BaseURL,
				Region:         item.Region,
				EgressIP:       item.EgressIP,
				Weight:         item.Weight,
				MaxConcurrency: item.MaxConcurrency,
				Status:         item.Status,
				HealthStatus:   RelayNodeHealthHealthy,
			}
			node.ID = id
			if node.Status == "" {
				node.Status = RelayNodeStatusDisabled
			}
			if err := saveBaseModel(tx, &node); err != nil {
				return err
			}
			result["relay_nodes"]++
		}

		for _, item := range snapshot.NodeChannels {
			id, err := uuid.Parse(item.ID)
			if err != nil {
				return err
			}
			relayNodeID, err := uuid.Parse(item.RelayNodeID)
			if err != nil {
				return err
			}
			channelID, err := uuid.Parse(item.ChannelID)
			if err != nil {
				return err
			}
			binding := db.NodeChannel{
				RelayNodeID: relayNodeID,
				ChannelID:   channelID,
				Weight:      item.Weight,
				Enabled:     item.Enabled,
			}
			binding.ID = id
			if err := saveBaseModel(tx, &binding); err != nil {
				return err
			}
			result["node_channels"]++
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	for channelID := range refreshedChannels {
		if h.RefreshPool != nil {
			h.RefreshPool(channelID)
		}
	}
	result["refreshed_channels"] = len(refreshedChannels)
	return result, nil
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

func intString(value int) string {
	return fmt.Sprint(value)
}

func yamlValueString(value interface{}) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func restoreEncryptedValue(plain, stored string) (string, error) {
	if strings.TrimSpace(plain) != "" {
		return internalcrypto.Encrypt(plain)
	}
	if strings.TrimSpace(stored) != "" {
		return stored, nil
	}
	return "", fmt.Errorf("credential is required")
}

func restoreOptionalEncryptedValue(plain, stored string) (string, error) {
	if strings.TrimSpace(plain) != "" {
		return internalcrypto.Encrypt(plain)
	}
	return stored, nil
}

func saveBaseModel(tx *gorm.DB, value interface{}) error {
	return tx.Unscoped().Save(value).Error
}
