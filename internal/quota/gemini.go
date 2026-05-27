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

	quota, err := fetchGeminiQuota(accessToken, projectID)
	if err != nil {
		return nil, err
	}

	return convertGeminiQuota(quota), nil
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
	reqBody := map[string]interface{}{"project": projectID}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequest(http.MethodPost, gemini.CodeAssistEndpoint+"/"+gemini.CodeAssistVersion+":retrieveUserQuota", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "google-cloud-sdk")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("retrieveUserQuota request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read retrieveUserQuota response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("retrieveUserQuota failed: status %d: %s", resp.StatusCode, truncate(respBody, 200))
	}
	var quota map[string]interface{}
	if err := json.Unmarshal(respBody, &quota); err != nil {
		return nil, fmt.Errorf("parse retrieveUserQuota response: %w", err)
	}
	return quota, nil
}

func convertGeminiQuota(raw map[string]interface{}) *QuotaData {
	qd := &QuotaData{}

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
			label := "额度桶"
			if model, ok := bucket["modelId"].(string); ok && model != "" {
				label = model
			} else if name, ok := bucket["tokenType"].(string); ok && name != "" {
				label = name
			} else {
				label = fmt.Sprintf("额度桶 %d", i+1)
			}
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
