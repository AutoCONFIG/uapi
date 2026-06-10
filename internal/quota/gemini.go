package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gemini "github.com/AutoCONFIG/uapi/internal/relay/provider/gemini"
)

func init() {
	Register("gemini_code", &geminiFetcher{})
}

type geminiFetcher struct{}

func (g *geminiFetcher) FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error) {
	projectID := geminiProjectID(metadata)
	if projectID == "" {
		return nil, nil
	}

	quota, debugInfo, err := fetchGeminiQuotaWithDebug(accessToken, projectID)
	if err != nil {
		writeQuotaDebugDump("gemini_code", metadata, debugInfo, nil, err)
		return nil, err
	}

	qd := convertGeminiQuota(quota)
	writeQuotaDebugDump("gemini_code", metadata, debugInfo, qd, nil)
	return qd, nil
}

func geminiProjectID(metadata map[string]interface{}) string {
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
	}
	return ""
}

func fetchGeminiQuota(accessToken, projectID string) (map[string]interface{}, error) {
	quota, _, err := fetchGeminiQuotaWithDebug(accessToken, projectID)
	return quota, err
}

func fetchGeminiQuotaWithDebug(accessToken, projectID string) (map[string]interface{}, *quotaHTTPDebug, error) {
	reqBody := map[string]interface{}{"project": projectID}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequest(http.MethodPost, gemini.CodeAssistEndpoint+"/"+gemini.CodeAssistVersion+":retrieveUserQuota", strings.NewReader(string(body)))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "google-cloud-sdk")
	debugInfo := newQuotaHTTPDebug(req, body)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, debugInfo, fmt.Errorf("retrieveUserQuota request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, debugInfo, fmt.Errorf("read retrieveUserQuota response: %w", err)
	}
	finishQuotaHTTPDebug(debugInfo, resp, respBody)
	if resp.StatusCode == 403 {
		return map[string]interface{}{"_forbidden": true, "_forbidden_reason": "account_forbidden"}, debugInfo, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, debugInfo, fmt.Errorf("retrieveUserQuota failed: status %d: %s", resp.StatusCode, truncate(respBody, 200))
	}
	var quota map[string]interface{}
	if err := json.Unmarshal(respBody, &quota); err != nil {
		return nil, debugInfo, fmt.Errorf("parse retrieveUserQuota response: %w", err)
	}
	return quota, debugInfo, nil
}

func convertGeminiQuota(raw map[string]interface{}) *QuotaData {
	qd := &QuotaData{}

	// Check for forbidden flag
	if forbidden, ok := raw["_forbidden"].(bool); ok && forbidden {
		qd.IsForbidden = true
		qd.ForbiddenReason, _ = raw["_forbidden_reason"].(string)
		return qd
	}

	if buckets, ok := raw["buckets"].([]interface{}); ok {
		for i, b := range buckets {
			bucket, ok := b.(map[string]interface{})
			if !ok {
				continue
			}
			var remainingPercent int
			if frac, ok := bucket["remainingFraction"].(float64); ok {
				remainingPercent = int(frac * 100)
			}
			if remainingPercent < 0 {
				remainingPercent = 0
			}
			if remainingPercent > 100 {
				remainingPercent = 100
			}
			// Generate friendly Chinese label based on model name
			label := geminiBucketLabel(bucket, i)
			var resetTime string
			if rt, ok := bucket["resetTime"].(string); ok {
				resetTime = rt
			}
			qd.Buckets = append(qd.Buckets, QuotaBucket{
				Label:            label,
				RemainingPercent: remainingPercent,
				ResetTime:        resetTime,
			})
		}
	}

	return qd
}

func geminiBucketLabel(bucket map[string]interface{}, index int) string {
	model, _ := bucket["modelId"].(string)
	tokenType, _ := bucket["tokenType"].(string)

	// Determine bucket type based on model name
	// Gemini has three main quota types: Pro, Flash, and Lite
	if model != "" {
		lowerModel := strings.ToLower(model)
		if strings.Contains(lowerModel, "pro") {
			return "Pro 额度"
		} else if strings.Contains(lowerModel, "flash") {
			return "Flash 额度"
		} else if strings.Contains(lowerModel, "lite") {
			return "Lite 额度"
		}
		return fmt.Sprintf("%s 额度", model)
	}

	if tokenType != "" {
		// Token type like "rpv" (requests per volume) or "rpm" (requests per minute)
		return fmt.Sprintf("Gemini %s", tokenType)
	}

	return fmt.Sprintf("Gemini 额度桶 %d", index+1)
}
