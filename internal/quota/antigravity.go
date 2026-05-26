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
	for _, endpoint := range antigravityQuotaEndpoints {
		m, err := fetchAntigravityModels(endpoint, accessToken, body)
		if err != nil {
			lastErr = err
			continue
		}
		models = m
		lastErr = nil
		break
	}
	if lastErr != nil {
		return nil, fmt.Errorf("all antigravity quota endpoints failed: %w", lastErr)
	}

	return convertAntigravityModels(models, metadata), nil
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
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", antigravity.RequestUserAgent())

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("forbidden")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(respBody, 200))
	}

	return parseAntigravityModels(respBody)
}

func parseAntigravityModels(data []byte) ([]modelEntry, error) {
	// Try array format: models[].id
	var arrResp struct {
		Models []struct {
			ID                string  `json:"id"`
			RemainingFraction float64 `json:"remaining_fraction"`
			ResetTime         string  `json:"reset_time"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &arrResp); err == nil && len(arrResp.Models) > 0 {
		var out []modelEntry
		for _, m := range arrResp.Models {
			out = append(out, modelEntry{Name: m.ID, RemainingFraction: m.RemainingFraction, ResetTime: m.ResetTime})
		}
		return out, nil
	}

	// Try map format: keys are model IDs
	var mapResp map[string]json.RawMessage
	if err := json.Unmarshal(data, &mapResp); err == nil {
		var out []modelEntry
		for name, raw := range mapResp {
			var entry modelEntry
			if err := json.Unmarshal(raw, &entry); err != nil {
				continue
			}
			entry.Name = name
			out = append(out, entry)
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	return nil, fmt.Errorf("no models found in response")
}

func convertAntigravityModels(models []modelEntry, metadata map[string]interface{}) *QuotaData {
	qd := &QuotaData{}

	for _, m := range models {
		name := strings.TrimPrefix(m.Name, "models/")
		if !isRelevantModel(name) {
			continue
		}
		pct := int(m.RemainingFraction * 100)
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		qd.Buckets = append(qd.Buckets, QuotaBucket{
			Label:            name,
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

func isRelevantModel(name string) bool {
	prefixes := []string{"gemini", "claude", "gpt", "image", "imagen"}
	lower := strings.ToLower(name)
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
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

