package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	antigravity "github.com/AutoCONFIG/uapi/internal/relay/provider/antigravity"
)

func init() {
	Register("antigravity", &antigravityFetcher{})
}

var antigravityQuotaEndpoints = []string{
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:fetchAvailableModels",
	"https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
	"https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
}

type antigravityFetcher struct{}

func (a *antigravityFetcher) FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error) {
	projectID := antigravityProjectIDFromMeta(metadata)

	var body []byte
	if strings.TrimSpace(projectID) != "" {
		body, _ = json.Marshal(map[string]string{"project": strings.TrimSpace(projectID)})
	} else {
		body = []byte(`{}`)
	}

	var models []modelEntry
	var lastErr error
	var debugAttempts []*quotaHTTPDebug
	forbidden := false
	for _, endpoint := range antigravityQuotaEndpoints {
		m, debugInfo, err := fetchAntigravityModelsWithDebug(endpoint, accessToken, body)
		debugAttempts = append(debugAttempts, debugInfo)
		if err != nil {
			if strings.Contains(err.Error(), "forbidden") {
				forbidden = true
				continue
			}
			lastErr = err
			continue
		}
		models = m
		lastErr = nil
		forbidden = false
		break
	}

	// All endpoints returned 403 → account is forbidden
	if forbidden && models == nil {
		qd := &QuotaData{
			IsForbidden:     true,
			ForbiddenReason: "account_forbidden",
		}
		writeQuotaDebugDump("antigravity", metadata, debugAttempts, qd, nil)
		return qd, nil
	}

	if lastErr != nil {
		writeQuotaDebugDump("antigravity", metadata, debugAttempts, nil, lastErr)
		return nil, fmt.Errorf("all antigravity quota endpoints failed: %w", lastErr)
	}

	qd := convertAntigravityModels(models, metadata)
	writeQuotaDebugDump("antigravity", metadata, debugAttempts, qd, nil)
	return qd, nil
}

func antigravityProjectIDFromMeta(metadata map[string]interface{}) string {
	if pid, ok := metadata["project_id"].(string); ok && pid != "" {
		return pid
	}
	lca, ok := metadata["load_code_assist"].(map[string]interface{})
	if !ok {
		return ""
	}
	if pid, ok := lca["cloudaicompanionProject"].(string); ok && pid != "" {
		return pid
	}
	if m, ok := lca["cloudaicompanionProject"].(map[string]interface{}); ok {
		if pid, ok := m["id"].(string); ok && pid != "" {
			return pid
		}
		if pid, ok := m["projectId"].(string); ok && pid != "" {
			return pid
		}
	}
	return ""
}

type modelEntry struct {
	Name              string  `json:"name"`
	RemainingFraction float64 `json:"remaining_fraction"`
	ResetTime         string  `json:"reset_time"`
}

func fetchAntigravityModels(endpoint, accessToken string, body []byte) ([]modelEntry, error) {
	models, _, err := fetchAntigravityModelsWithDebug(endpoint, accessToken, body)
	return models, err
}

func fetchAntigravityModelsWithDebug(endpoint, accessToken string, body []byte) ([]modelEntry, *quotaHTTPDebug, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", antigravity.RequestUserAgent())
	debugInfo := newQuotaHTTPDebug(req, body)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, debugInfo, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, debugInfo, fmt.Errorf("read response: %w", err)
	}
	finishQuotaHTTPDebug(debugInfo, resp, respBody)
	if resp.StatusCode == 403 {
		return nil, debugInfo, fmt.Errorf("forbidden")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, debugInfo, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(respBody, 200))
	}

	models, err := parseAntigravityModels(respBody)
	if err != nil {
		return nil, debugInfo, err
	}
	return models, debugInfo, nil
}

func parseAntigravityModels(data []byte) ([]modelEntry, error) {
	var arrResp struct {
		Models []map[string]interface{} `json:"models"`
	}
	if err := json.Unmarshal(data, &arrResp); err == nil && len(arrResp.Models) > 0 {
		out := antigravityEntriesFromArray(arrResp.Models)
		if len(out) > 0 {
			return out, nil
		}
	}

	var objectResp struct {
		Models map[string]map[string]interface{} `json:"models"`
	}
	if err := json.Unmarshal(data, &objectResp); err == nil && len(objectResp.Models) > 0 {
		out := make([]modelEntry, 0, len(objectResp.Models))
		for name, value := range objectResp.Models {
			if entry, ok := antigravityEntryFromMap(name, value); ok {
				out = append(out, entry)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	var array []map[string]interface{}
	if err := json.Unmarshal(data, &array); err == nil && len(array) > 0 {
		out := antigravityEntriesFromArray(array)
		return out, nil
	}

	// Try map format: keys are model IDs
	var mapResp map[string]json.RawMessage
	if err := json.Unmarshal(data, &mapResp); err == nil {
		var out []modelEntry
		for name, raw := range mapResp {
			var value map[string]interface{}
			if err := json.Unmarshal(raw, &value); err != nil {
				continue
			}
			if entry, ok := antigravityEntryFromMap(name, value); ok {
				out = append(out, entry)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	return nil, fmt.Errorf("no models found in response")
}

func antigravityEntriesFromArray(models []map[string]interface{}) []modelEntry {
	out := make([]modelEntry, 0, len(models))
	for _, m := range models {
		if entry, ok := antigravityEntryFromMap("", m); ok {
			out = append(out, entry)
		}
	}
	return out
}

func antigravityEntryFromMap(fallbackName string, m map[string]interface{}) (modelEntry, bool) {
	name := firstString(m, "id", "name", "model", "modelId", "model_id")
	if name == "" {
		name = fallbackName
	}
	if name == "" {
		return modelEntry{}, false
	}
	source := m
	for _, key := range []string{"quotaInfo", "quota_info", "quota", "usage", "rateLimit", "rate_limit"} {
		if nested := mapValue(m, key); nested != nil {
			source = nested
			break
		}
	}
	remaining := firstFloat(source, "remaining_fraction", "remainingFraction")
	if remaining == nil {
		if pct := firstFloat(source, "remaining_percent", "remainingPercent"); pct != nil {
			value := *pct / 100
			remaining = &value
		}
	}
	if remaining == nil {
		if used := firstFloat(source, "used_percent", "usedPercent", "utilization"); used != nil {
			value := (100 - *used) / 100
			remaining = &value
		}
	}
	if remaining == nil {
		return modelEntry{}, false
	}
	return modelEntry{
		Name:              name,
		RemainingFraction: *remaining,
		ResetTime:         firstString(source, "reset_time", "resetTime", "resets_at", "reset_at", "resetAt"),
	}, true
}

func convertAntigravityModels(models []modelEntry, metadata map[string]interface{}) *QuotaData {
	qd := &QuotaData{}
	indexByKey := map[string]int{}

	for _, m := range models {
		name := strings.TrimPrefix(m.Name, "models/")
		spec, ok := antigravity.CanonicalModel(name)
		label := antigravity.DisplayName(name)
		dedupeKey := name
		if ok {
			dedupeKey = spec.ID
		}
		pct := int(m.RemainingFraction * 100)
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		if idx, exists := indexByKey[dedupeKey]; exists {
			if pct < qd.Buckets[idx].RemainingPercent {
				qd.Buckets[idx].RemainingPercent = pct
			}
			if resetUnix(m.ResetTime) > 0 && (resetUnix(qd.Buckets[idx].ResetTime) == 0 || resetUnix(m.ResetTime) < resetUnix(qd.Buckets[idx].ResetTime)) {
				qd.Buckets[idx].ResetTime = m.ResetTime
			}
			continue
		}
		indexByKey[dedupeKey] = len(qd.Buckets)
		qd.Buckets = append(qd.Buckets, QuotaBucket{
			Label:            label,
			RemainingPercent: pct,
			ResetTime:        m.ResetTime,
		})
	}

	// Extract tier and credits from load_code_assist metadata
	if lca, ok := metadata["load_code_assist"].(map[string]interface{}); ok {
		if tier, ok := lca["paidTier"].(map[string]interface{}); ok {
			if id, ok := tier["id"].(string); ok {
				qd.Tier = id
			}
			qd.Credits = extractCredits(tier)
		}
	}

	return qd
}

func extractCredits(paidTier map[string]interface{}) *CreditsInfo {
	credits, ok := paidTier["availableCredits"].([]interface{})
	if !ok {
		return nil
	}
	for _, c := range credits {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		ct, _ := cm["creditType"].(string)
		if ct != "GOOGLE_ONE_AI" {
			continue
		}
		amount, _ := cm["creditAmount"].(string)
		return &CreditsInfo{
			Balance:   amount,
			Label:     "G1 AI Credits",
			Unlimited: false,
		}
	}
	return nil
}
