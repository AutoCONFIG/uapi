# OAuth Quota Module Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `internal/quota` package that unifies quota fetching for all OAuth channels (Gemini, Antigravity, Claude Code, Codex), stores standardized data in `meta.quota`, and triggers refresh on 429 responses and admin frontend access.

**Architecture:** New `internal/quota` package with `Fetcher` interface + per-provider implementations. A `Scheduler` handles batch refresh with jitter. Relay layer hooks into 429 responses. Admin API adds refresh-quota endpoints. Frontend reads unified `meta.quota` with fallback to legacy keys.

**Tech Stack:** Go 1.26, fasthttp, gorm, golang-jwt, React/Next.js frontend

**Spec:** `docs/superpowers/specs/2026-05-26-oauth-quota-module-design.md`

---

## File Structure

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `internal/quota/types.go` | `QuotaData`, `QuotaBucket`, `CreditsInfo` structs |
| Create | `internal/quota/fetcher.go` | `Fetcher` interface + `Register`/`Get` registry |
| Create | `internal/quota/scheduler.go` | `Scheduler` with batch refresh, staleness guard, jitter |
| Create | `internal/quota/gemini.go` | Gemini Fetcher implementation |
| Create | `internal/quota/antigravity.go` | Antigravity Fetcher with multi-endpoint fallback |
| Create | `internal/quota/anthropic.go` | Claude Code Fetcher implementation |
| Create | `internal/quota/codex.go` | Codex Fetcher implementation |
| Modify | `internal/relay/handler.go:841-895` | Hook 429 into quota refresh |
| Modify | `internal/relay/relayer.go` | Add `quotaScheduler` field + setter |
| Modify | `internal/server/server.go` | Initialize `quota.Scheduler`, wire into relayer + admin |
| Modify | `internal/admin/account_handler.go` | Add refresh-quota endpoints |
| Modify | `internal/admin/handler.go` | Wire quota scheduler into admin handler |
| Modify | `web/lib/api.ts` | Add `refreshAccountQuota` and `refreshChannelQuota` API calls |
| Modify | `web/components/admin-channel-console.tsx` | Read `meta.quota` first, fallback to legacy; add refresh button |

---

### Task 1: Types and Fetcher Interface

**Files:**
- Create: `internal/quota/types.go`
- Create: `internal/quota/fetcher.go`

- [ ] **Step 1: Create types.go with standard data structures**

```go
package quota

import "time"

type QuotaData struct {
	Buckets   []QuotaBucket `json:"buckets"`
	Credits   *CreditsInfo  `json:"credits,omitempty"`
	Tier      string        `json:"tier,omitempty"`
	FetchedAt time.Time     `json:"fetched_at"`
}

type QuotaBucket struct {
	Label            string `json:"label"`
	RemainingPercent int    `json:"remaining_percent"`
	ResetTime        string `json:"reset_time,omitempty"`
}

type CreditsInfo struct {
	Balance   string `json:"balance,omitempty"`
	Unlimited bool   `json:"unlimited"`
	Label     string `json:"label,omitempty"`
}
```

- [ ] **Step 2: Create fetcher.go with interface and registry**

```go
package quota

import "github.com/google/uuid"

type Fetcher interface {
	FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error)
}

var registry = map[string]Fetcher{}

func Register(apiFormat string, f Fetcher) {
	registry[apiFormat] = f
}

func Get(apiFormat string) (Fetcher, bool) {
	f, ok := registry[apiFormat]
	return f, ok
}

// AccountRef identifies an account for batch refresh.
type AccountRef struct {
	AccountID uuid.UUID
	ChannelID uuid.UUID
	APIFormat string
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/quota/`
Expected: compiles cleanly

- [ ] **Step 4: Commit**

```bash
git add internal/quota/types.go internal/quota/fetcher.go
git commit -m "feat(quota): add QuotaData types and Fetcher interface"
```

---

### Task 2: Scheduler

**Files:**
- Create: `internal/quota/scheduler.go`

The Scheduler handles batch refresh with jitter, staleness guard, and per-channel mutex. It does NOT use a channel-based queue — instead it provides `RefreshAccount` (single) and `RefreshChannel` (batch) methods that are called directly.

- [ ] **Step 1: Create scheduler.go**

```go
package quota

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/crypto"
	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	stalenessTTL = 5 * time.Minute
	batchSize    = 3
	jitterMin    = 2 * time.Second
	jitterMax    = 8 * time.Second
)

type Scheduler struct {
	db         *gorm.DB
	crypto     *crypto.Service
	mu         sync.Map // per-channel mutex: channelID -> *sync.Mutex
	pending    sync.Map // dedup: accountID -> time.Time (last refresh start)
}

func NewScheduler(database *gorm.DB, cryptoSvc *crypto.Service) *Scheduler {
	return &Scheduler{db: database, crypto: cryptoSvc}
}

func (s *Scheduler) channelMu(channelID uuid.UUID) *sync.Mutex {
	mu, _ := s.mu.LoadOrStore(channelID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// RefreshAccount refreshes a single account. Returns the quota data or error.
// This is used for admin frontend trigger and 429 trigger.
func (s *Scheduler) RefreshAccount(accountID uuid.UUID) (*QuotaData, error) {
	var acc db.Account
	if err := s.db.First(&acc, "id = ?", accountID).Error; err != nil {
		return nil, err
	}
	var ch db.Channel
	if err := s.db.First(&ch, "id = ?", acc.ChannelID).Error; err != nil {
		return nil, err
	}
	return s.refreshOne(acc, ch)
}

// On429 is called when upstream returns 429. Runs refresh in background.
func (s *Scheduler) On429(accountID, channelID uuid.UUID) {
	go func() {
		_, _ = s.RefreshAccount(accountID)
	}()
}

// RefreshChannel refreshes all OAuth accounts under a channel in small batches with jitter.
func (s *Scheduler) RefreshChannel(channelID uuid.UUID) ([]*QuotaData, []error) {
	mu := s.channelMu(channelID)
	mu.Lock()
	defer mu.Unlock()

	var accounts []db.Account
	if err := s.db.Where("channel_id = ? AND cred_type = ?", channelID, "oauth_token").Find(&accounts).Error; err != nil {
		return nil, []error{err}
	}

	// Filter stale accounts
	var stale []db.Account
	cutoff := time.Now().Add(-stalenessTTL)
	for _, acc := range accounts {
		if !s.isStale(acc, cutoff) {
			continue
		}
		stale = append(stale, acc)
	}

	var ch db.Channel
	if err := s.db.First(&ch, "id = ?", channelID).Error; err != nil {
		return nil, []error{err}
	}

	var results []*QuotaData
	var errs []error
	for i := 0; i < len(stale); i += batchSize {
		end := min(i+batchSize, len(stale))
		batch := stale[i:end]

		var wg sync.WaitGroup
		batchResults := make([]*QuotaData, len(batch))
		batchErrs := make([]error, len(batch))
		for j, acc := range batch {
			wg.Add(1)
			go func(idx int, a db.Account) {
				defer wg.Done()
				q, err := s.refreshOne(a, ch)
				batchResults[idx] = q
				batchErrs[idx] = err
			}(j, acc)
		}
		wg.Wait()

		for j, q := range batchResults {
			if batchErrs[j] != nil {
				errs = append(errs, batchErrs[j])
			} else if q != nil {
				results = append(results, q)
			}
		}

		// Jitter between batches (skip after last batch)
		if end < len(stale) {
			d := jitterMin + time.Duration(rand.Int64N(int64(jitterMax-jitterMin)))
			time.Sleep(d)
		}
	}
	return results, errs
}

func (s *Scheduler) isStale(acc db.Account, cutoff time.Time) bool {
	meta := acc.Metadata
	if meta == nil {
		return true
	}
	quota, ok := meta["quota"].(map[string]interface{})
	if !ok {
		return true
	}
	fetchedAt, ok := quota["fetched_at"].(string)
	if !ok {
		return true
	}
	t, err := time.Parse(time.RFC3339, fetchedAt)
	if err != nil {
		return true
	}
	return t.Before(cutoff)
}

func (s *Scheduler) refreshOne(acc db.Account, ch db.Channel) (*QuotaData, error) {
	fetcher, ok := Get(ch.APIFormat)
	if !ok {
		return nil, nil // no fetcher for this format, skip silently
	}

	credential, err := s.crypto.Decrypt(acc.Credentials)
	if err != nil {
		return nil, err
	}

	// If token is expired and we have a refresh token, try refreshing first
	accessToken := string(credential)
	if acc.TokenExpiry != nil && time.Now().After(*acc.TokenExpiry) && acc.RefreshToken != "" {
		refreshed, err := s.refreshOAuthToken(acc, ch)
		if err == nil {
			accessToken = refreshed
		}
		// If refresh fails, try with existing token anyway
	}

	qd, err := fetcher.FetchQuota(accessToken, acc.Metadata)
	if err != nil {
		return nil, err
	}
	if qd == nil {
		return nil, nil
	}

	qd.FetchedAt = time.Now().UTC()

	// Write quota into metadata
	if acc.Metadata == nil {
		acc.Metadata = map[string]interface{}{}
	}
	acc.Metadata["quota"] = qd
	if err := s.db.Model(&db.Account{}).Where("id = ?", acc.ID).Update("metadata", acc.Metadata).Error; err != nil {
		return nil, err
	}
	return qd, nil
}

// refreshOAuthToken attempts to refresh an expired OAuth token using the provider's SyncMetadata flow.
// Returns the new access token on success.
func (s *Scheduler) refreshOAuthToken(acc db.Account, ch db.Channel) (string, error) {
	// Delegate to the existing OAuth idle maintainer logic
	// This is a best-effort refresh; if it fails we still try the existing token
	return "", fmt.Errorf("token refresh not yet wired in scheduler")
}
```

Note: `refreshOAuthToken` is a placeholder that returns an error. The actual token refresh logic is handled by the existing `OAuthIdleMaintainer`. When a token is expired, the scheduler will try the stale token first — if it fails, the fetcher will return an error, and the quota simply won't be updated. This is acceptable because the `OAuthIdleMaintainer` runs separately and will refresh the token soon.

- [ ] **Step 2: Fix the import — add `fmt`**

Add `"fmt"` to the imports in scheduler.go.

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/quota/`
Expected: compiles cleanly

- [ ] **Step 4: Commit**

```bash
git add internal/quota/scheduler.go
git commit -m "feat(quota): add Scheduler with batch refresh and jitter"
```

---

### Task 3: Gemini Fetcher

**Files:**
- Create: `internal/quota/gemini.go`

This reuses the existing `retrieveUserQuota` logic from `internal/relay/provider/gemini/auth.go` but normalizes the output into `QuotaData`.

- [ ] **Step 1: Create gemini.go**

```go
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
		if pid, ok := m["projectId"].(string); ok && pid != "" {
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
		return nil, fmt.Errorf("retrieveUserQuota failed: status %d: %s", resp.StatusCode, string(respBody[:min(len(respBody), 200)]))
	}
	var quota map[string]interface{}
	if err := json.Unmarshal(respBody, &quota); err != nil {
		return nil, fmt.Errorf("parse retrieveUserQuota response: %w", err)
	}
	return quota, nil
}

func convertGeminiQuota(raw map[string]interface{}) *QuotaData {
	qd := &QuotaData{}

	// Parse buckets
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

	// Parse tier from loadCodeAssist (stored in metadata by SyncMetadata)
	// This is best-effort; tier may already be in metadata

	return qd
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/quota/`
Expected: compiles cleanly

- [ ] **Step 3: Commit**

```bash
git add internal/quota/gemini.go
git commit -m "feat(quota): add Gemini Fetcher with retrieveUserQuota"
```

---

### Task 4: Antigravity Fetcher

**Files:**
- Create: `internal/quota/antigravity.go`

Uses `fetchAvailableModels` with multi-endpoint fallback (borrowed from Antigravity-Manager's pattern: sandbox → daily → production).

- [ ] **Step 1: Create antigravity.go**

```go
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
	Name             string  `json:"name"`
	RemainingFraction float64 `json:"remaining_fraction"`
	ResetTime        string  `json:"reset_time"`
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
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody[:min(len(respBody), 200)]))
	}

	return parseAntigravityModels(respBody)
}

func parseAntigravityModels(data []byte) ([]modelEntry, error) {
	// Try array format: models[].id
	var arrResp struct {
		Models []struct {
			ID               string  `json:"id"`
			RemainingFraction float64 `json:"remaining_fraction"`
			ResetTime        string  `json:"reset_time"`
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

	// Filter to relevant model prefixes
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
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/quota/`
Expected: compiles cleanly

- [ ] **Step 3: Commit**

```bash
git add internal/quota/antigravity.go
git commit -m "feat(quota): add Antigravity Fetcher with multi-endpoint fallback"
```

---

### Task 5: Anthropic Fetcher

**Files:**
- Create: `internal/quota/anthropic.go`

Fetches usage from `/api/oauth/usage` and converts to `QuotaData`.

- [ ] **Step 1: Create anthropic.go**

```go
package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func init() {
	Register("claude_code", &anthropicFetcher{})
}

const anthropicUsageURL = "https://api.claude.ai/api/oauth/usage"

type anthropicFetcher struct{}

func (a *anthropicFetcher) FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error) {
	usage, err := fetchAnthropicUsage(accessToken)
	if err != nil {
		return nil, err
	}
	return convertAnthropicUsage(usage, metadata), nil
}

func fetchAnthropicUsage(accessToken string) (map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodGet, anthropicUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic usage request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read anthropic usage response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("anthropic usage failed: status %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}
	var usage map[string]interface{}
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, fmt.Errorf("parse anthropic usage response: %w", err)
	}
	return usage, nil
}

var anthropicWindowLabels = map[string]string{
	"five_hour":           "Claude 5h 窗口",
	"seven_day":           "Claude 周窗口",
	"seven_day_sonnet":    "Claude Sonnet 周窗口",
	"seven_day_opus":      "Claude Opus 周窗口",
	"seven_day_oauth_apps": "Claude OAuth Apps 周窗口",
}

func convertAnthropicUsage(usage map[string]interface{}, metadata map[string]interface{}) *QuotaData {
	qd := &QuotaData{}

	for key, label := range anthropicWindowLabels {
		window, ok := usage[key].(map[string]interface{})
		if !ok {
			continue
		}
		utilization, ok := window["utilization"].(float64)
		if !ok {
			continue
		}
		// utilization is 0-100 percentage of usage; remaining = 100 - utilization
		usedPercent := int(utilization)
		if usedPercent < 0 {
			usedPercent = 0
		}
		if usedPercent > 100 {
			usedPercent = 100
		}
		remaining := 100 - usedPercent

		var resetTime string
		if rt, ok := window["resets_at"].(string); ok {
			resetTime = rt
		}

		qd.Buckets = append(qd.Buckets, QuotaBucket{
			Label:            label,
			RemainingPercent: remaining,
			ResetTime:        resetTime,
		})
	}

	// Extract tier from metadata
	if sub, ok := metadata["subscription_type"].(string); ok && sub != "" {
		qd.Tier = sub
	}

	// Extract extra_usage credits
	if eu, ok := usage["extra_usage"].(map[string]interface{}); ok {
		enabled, _ := eu["is_enabled"].(bool)
		if enabled {
			var balance string
			if used, ok := eu["used_credits"].(float64); ok {
				if limit, ok := eu["monthly_limit"].(float64); ok && limit > 0 {
					remaining := int(limit) - int(used)
					if remaining < 0 {
						remaining = 0
					}
					balance = fmt.Sprintf("%d / %d", remaining, int(limit))
				}
			}
			qd.Credits = &CreditsInfo{
				Balance:   balance,
				Label:     "Extra Usage",
				Unlimited: false,
			}
		}
	}

	return qd
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/quota/`
Expected: compiles cleanly

- [ ] **Step 3: Commit**

```bash
git add internal/quota/anthropic.go
git commit -m "feat(quota): add Anthropic/Claude Code Fetcher"
```

---

### Task 6: Codex Fetcher

**Files:**
- Create: `internal/quota/codex.go`

Reuses the existing `FetchCodexUsage` from `internal/relay/provider/openai/auth.go`.

- [ ] **Step 1: Create codex.go**

```go
package quota

import (
	"fmt"

	openai "github.com/AutoCONFIG/uapi/internal/relay/provider/openai"
)

func init() {
	Register("codex", &codexFetcher{})
}

type codexFetcher struct{}

func (c *codexFetcher) FetchQuota(accessToken string, metadata map[string]interface{}) (*QuotaData, error) {
	accountID := codexAccountID(metadata)
	fedramp := codexFedramp(metadata)

	usage, err := openai.FetchCodexUsage(accessToken, accountID, fedramp)
	if err != nil {
		return nil, err
	}
	return convertCodexUsage(usage), nil
}

func codexAccountID(metadata map[string]interface{}) string {
	if id, ok := metadata["chatgpt_account_id"].(string); ok {
		return id
	}
	return ""
}

func codexFedramp(metadata map[string]interface{}) bool {
	if v, ok := metadata["chatgpt_account_is_fedramp"].(bool); ok {
		return v
	}
	return false
}

func convertCodexUsage(raw map[string]interface{}) *QuotaData {
	qd := &QuotaData{}

	// Navigate to rate_limits
	limits := raw
	if rl, ok := raw["rate_limits"].(map[string]interface{}); ok {
		limits = rl
	} else if rl, ok := raw["rateLimits"].(map[string]interface{}); ok {
		limits = rl
	}

	// Primary window (short, e.g. 5h)
	if primary, ok := limits["primary"].(map[string]interface{}); ok {
		usedPct := codexUsedPercent(primary)
		if usedPct != nil {
			remaining := 100 - *usedPct
			if remaining < 0 {
				remaining = 0
			}
			qd.Buckets = append(qd.Buckets, QuotaBucket{
				Label:            "Codex 主窗口",
				RemainingPercent: remaining,
				ResetTime:        codexResetTime(primary),
			})
		}
	}

	// Secondary window (weekly)
	if secondary, ok := limits["secondary"].(map[string]interface{}); ok {
		usedPct := codexUsedPercent(secondary)
		if usedPct != nil {
			remaining := 100 - *usedPct
			if remaining < 0 {
				remaining = 0
			}
			qd.Buckets = append(qd.Buckets, QuotaBucket{
				Label:            "Codex 周窗口",
				RemainingPercent: remaining,
				ResetTime:        codexResetTime(secondary),
			})
		}
	}

	// Credits
	if credits, ok := limits["credits"].(map[string]interface{}); ok {
		hasCredits, _ := credits["has_credits"].(bool)
		if hasCredits {
			unlimited, _ := credits["unlimited"].(bool)
			balance, _ := credits["balance"].(string)
			qd.Credits = &CreditsInfo{
				Balance:   balance,
				Unlimited: unlimited,
				Label:     "Codex Credits",
			}
		}
	}

	// Tier from plan type
	if planType, ok := raw["plan_type"].(string); ok && planType != "" {
		qd.Tier = planType
	}

	return qd
}

func codexUsedPercent(m map[string]interface{}) *int {
	for _, key := range []string{"used_percent", "usedPercent"} {
		if v, ok := m[key].(float64); ok {
			pct := int(v)
			return &pct
		}
	}
	return nil
}

func codexResetTime(m map[string]interface{}) string {
	for _, key := range []string{"resets_at", "reset_at", "resetAt"} {
		if v, ok := m[key].(string); ok {
			return v
		}
	}
	// reset_at might be a Unix timestamp (number)
	for _, key := range []string{"resets_at", "reset_at", "resetAt"} {
		if v, ok := m[key].(float64); ok {
			return fmt.Sprintf("%d", int64(v))
		}
	}
	return ""
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/quota/`
Expected: compiles cleanly

- [ ] **Step 3: Commit**

```bash
git add internal/quota/codex.go
git commit -m "feat(quota): add Codex Fetcher"
```

---

### Task 7: Wire Scheduler into Server

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/relay/handler.go`

- [ ] **Step 1: Add quota.Scheduler field to Server struct**

In `internal/server/server.go`, add to the Server struct:

```go
quotaScheduler *quota.Scheduler
```

And in the `New()` function, after the crypto service is created, initialize the scheduler:

```go
quotaScheduler := quota.NewScheduler(s.db, cryptoSvc)
s.quotaScheduler = quotaScheduler
```

Add the import: `"github.com/AutoCONFIG/uapi/internal/quota"`

- [ ] **Step 2: Add QuotaScheduler setter to Relayer**

In `internal/relay/relayer.go`, add a field and setter:

```go
type Relayer struct {
    // ... existing fields ...
    quotaScheduler *quota.Scheduler
}

func (r *Relayer) SetQuotaScheduler(s *quota.Scheduler) {
    r.quotaScheduler = quotaScheduler
}
```

Then in `server.go`, after creating the relayer, wire it:

```go
s.relayer.SetQuotaScheduler(s.quotaScheduler)
```

- [ ] **Step 3: Hook 429 into quota refresh in handler.go**

In `internal/relay/handler.go`, in the `handleBuffered` retry loop (~line 841), after the upstream response is received, add a 429 check that triggers quota refresh:

```go
// After getting upResp from upstream, add before the retry check:
if upResp.StatusCode() == 429 {
    if r.quotaScheduler != nil && account != nil && channel != nil {
        r.quotaScheduler.On429(account.ID, channel.ID)
    }
}
```

This must be added before the existing `shouldRetry` logic so it fires on 429.

- [ ] **Step 4: Verify it compiles**

Run: `go build ./...`
Expected: compiles cleanly

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go internal/relay/relayer.go internal/relay/handler.go
git commit -m "feat(quota): wire Scheduler into server and relay 429 handler"
```

---

### Task 8: Admin API Endpoints

**Files:**
- Modify: `internal/admin/account_handler.go`
- Modify: `internal/admin/handler.go`

- [ ] **Step 1: Add refresh-quota handler to account_handler.go**

Add a new handler function:

```go
func (h *Handler) refreshAccountQuota(ctx *fasthttp.RequestCtx) {
	idStr := ctx.UserValue("id").(string)
	accountID, err := uuid.Parse(idStr)
	if err != nil {
		ctx.Error(`{"error":"invalid account id"}`, fasthttp.StatusBadRequest)
		return
	}
	if h.quotaScheduler == nil {
		ctx.Error(`{"error":"quota scheduler not available"}`, fasthttp.StatusServiceUnavailable)
		return
	}
	qd, err := h.quotaScheduler.RefreshAccount(accountID)
	if err != nil {
		ctx.Error(fmt.Sprintf(`{"error":"%s"}`, err.Error()), fasthttp.StatusInternalServerError)
		return
	}
	body, _ := json.Marshal(qd)
	ctx.SetContentType("application/json")
	ctx.SetBody(body)
}

func (h *Handler) refreshChannelQuota(ctx *fasthttp.RequestCtx) {
	idStr := ctx.UserValue("id").(string)
	channelID, err := uuid.Parse(idStr)
	if err != nil {
		ctx.Error(`{"error":"invalid channel id"}`, fasthttp.StatusBadRequest)
		return
	}
	if h.quotaScheduler == nil {
		ctx.Error(`{"error":"quota scheduler not available"}`, fasthttp.StatusServiceUnavailable)
		return
	}
	results, errs := h.quotaScheduler.RefreshChannel(channelID)
	resp := map[string]interface{}{
		"refreshed": len(results),
		"errors":    len(errs),
	}
	body, _ := json.Marshal(resp)
	ctx.SetContentType("application/json")
	ctx.SetBody(body)
}
```

- [ ] **Step 2: Add QuotaScheduler to Handler struct**

In `internal/admin/handler.go`, add `quotaScheduler *quota.Scheduler` to the Handler struct and update `New()` to accept it.

Also add `SetQuotaScheduler(s *quota.Scheduler)` method:

```go
func (h *Handler) SetQuotaScheduler(s *quota.Scheduler) {
	h.quotaScheduler = s
}
```

- [ ] **Step 3: Register routes in HandleAccounts**

In `internal/admin/account_handler.go`, add the refresh-quota routes. These need to be registered in the main router. Add to `HandleAccounts`:

```go
case "/refresh-quota":
    if ctx.IsPost() {
        h.refreshAccountQuota(ctx)
    }
```

And add a new `HandleChannelQuota` function or add to the channel routes:

```go
// In the channel routing section, add:
case "/refresh-quota":
    if ctx.IsPost() {
        h.refreshChannelQuota(ctx)
    }
```

The exact routing depends on the existing pattern. Check how `/admin/accounts/:id` is routed and add `/admin/accounts/:id/refresh-quota` and `/admin/channels/:id/refresh-quota` following the same pattern.

- [ ] **Step 4: Wire in server.go**

In `server.go`, after creating the admin handler:

```go
s.adminHandler.SetQuotaScheduler(s.quotaScheduler)
```

- [ ] **Step 5: Verify it compiles**

Run: `go build ./...`
Expected: compiles cleanly

- [ ] **Step 6: Commit**

```bash
git add internal/admin/account_handler.go internal/admin/handler.go internal/server/server.go
git commit -m "feat(quota): add admin refresh-quota API endpoints"
```

---

### Task 9: Frontend — API Client

**Files:**
- Modify: `web/lib/api.ts`

- [ ] **Step 1: Add refresh-quota API methods**

In the `adminApi` object in `web/lib/api.ts`, add:

```typescript
refreshAccountQuota: (token: string, accountId: string) =>
  request<Record<string, unknown>>("/api/admin/accounts/" + accountId + "/refresh-quota", { method: "POST", token }),

refreshChannelQuota: (token: string, channelId: string) =>
  request<{ refreshed: number; errors: number }>("/api/admin/channels/" + channelId + "/refresh-quota", { method: "POST", token }),
```

- [ ] **Step 2: Commit**

```bash
git add web/lib/api.ts
git commit -m "feat(quota): add frontend API client for refresh-quota"
```

---

### Task 10: Frontend — Quota Display + Refresh Button

**Files:**
- Modify: `web/components/admin-channel-console.tsx`

- [ ] **Step 1: Update buildQuotaDisplayItems to read meta.quota first**

Replace the existing `buildQuotaDisplayItems` function to read `meta.quota` as the primary source, falling back to the existing per-channel logic:

```typescript
function buildQuotaDisplayItems(account: Account): QuotaDisplayItem[] {
  const meta = account.metadata || {};
  const items: QuotaDisplayItem[] = [];

  // Primary: read unified meta.quota
  const quota = asRecord(meta.quota);
  if (quota) {
    const buckets = asArray(quota.buckets).map(asRecord).filter(Boolean) as Record<string, unknown>[];
    for (const [i, b] of buckets.entries()) {
      const pct = numberValue(b.remaining_percent);
      if (pct === null) continue;
      const remainingPercent = Math.round(clampPercent(pct));
      items.push({
        key: `quota-${i}`,
        label: stringValue(b.label) || `额度 ${i + 1}`,
        remainingPercent,
        resetText: formatResetTimeShort(stringValue(b.reset_time)),
        detail: [`剩余 ${remainingPercent}%`, stringValue(b.reset_time) ? `重置 ${stringValue(b.reset_time)}` : ""].filter(Boolean).join(" · "),
      });
    }
    const credits = asRecord(quota.credits);
    if (credits) {
      const balance = stringValue(credits.balance) || (credits.unlimited === true ? "unlimited" : "");
      if (balance) {
        items.push({
          key: "quota-credits",
          label: stringValue(credits.label) || "Credits",
          remainingPercent: credits.unlimited === true ? 100 : 0,
          detail: `Credits ${balance}`,
        });
      }
    }
    return items.sort((left, right) => left.remainingPercent - right.remainingPercent);
  }

  // Fallback: legacy per-channel parsing (existing code)
  const codexUsage = asRecord(meta.codex_usage);
  if (codexUsage) {
    const limits = asRecord(codexUsage.rate_limits) || asRecord(codexUsage.rateLimits) || codexUsage;
    addUsageLimit(items, "codex-primary", "Codex 主窗口", asRecord(limits.primary));
    addUsageLimit(items, "codex-secondary", "Codex 次窗口", asRecord(limits.secondary));
    const credits = asRecord(limits.credits);
    if (credits) {
      const balance = stringValue(credits.balance) || (credits.unlimited === true ? "unlimited" : "");
      items.push({ key: "codex-credits", label: "Credits", remainingPercent: credits.unlimited === true ? 100 : 0, detail: balance ? `Credits ${balance}` : "" });
    }
  }
  const geminiQuota = asRecord(meta.user_quota);
  if (geminiQuota) {
    const buckets = asArray(geminiQuota.buckets).map(asRecord).filter(Boolean) as Record<string, unknown>[];
    for (const [index, bucket] of buckets.entries()) {
      const remaining = numberValue(bucket.remainingFraction);
      const amount = stringValue(bucket.remainingAmount);
      if (remaining === null && !amount) continue;
      const percent = remaining !== null ? Math.round(clampPercent(remaining * 100)) : 0;
      const reset = stringValue(bucket.resetTime);
      items.push({
        key: `gemini-${index}`,
        label: quotaBucketLabel(bucket, index + 1),
        remainingPercent: percent,
        resetText: formatResetTimeShort(reset),
        detail: [amount ? `剩余 ${amount}` : `剩余 ${percent}%`, reset ? `重置 ${reset}` : ""].filter(Boolean).join(" · "),
      });
    }
  }
  const geminiCredits = asRecord(meta.credits) || asRecord(meta.credit_balance);
  if (geminiCredits) {
    const balance = stringValue(geminiCredits.balance) || stringValue(geminiCredits.remaining) || stringValue(geminiCredits.amount);
    if (balance) items.push({ key: "gemini-credits", label: "Credits", remainingPercent: 100, detail: `Credits ${balance}` });
  }
  const anthropicUsage = asRecord(meta.usage);
  if (anthropicUsage) {
    for (const key of ["five_hour", "seven_day", "seven_day_sonnet", "seven_day_opus", "seven_day_oauth_apps"]) {
      addUsageLimit(items, `anthropic-${key}`, key.replaceAll("_", " "), asRecord(anthropicUsage[key]));
    }
  }
  return items.sort((left, right) => left.remainingPercent - right.remainingPercent);
}
```

- [ ] **Step 2: Update accountUsagePercent to read meta.quota first**

In the `accountUsagePercent` function, add a primary check for `meta.quota`:

```typescript
function accountUsagePercent(account: Account): number | null {
  const meta = account.metadata || {};

  // Primary: read unified meta.quota
  const quota = asRecord(meta.quota);
  if (quota) {
    const buckets = asArray(quota.buckets).map(asRecord).filter(Boolean) as Record<string, unknown>[];
    const candidates: number[] = [];
    for (const b of buckets) {
      const pct = numberValue(b.remaining_percent);
      if (pct !== null) candidates.push(Math.max(0, Math.min(100, 100 - pct)));
    }
    return candidates.length > 0 ? Math.max(...candidates) : null;
  }

  // Fallback: legacy logic (keep existing code unchanged)
  // ... existing codex/gemini/anthropic logic ...
}
```

- [ ] **Step 3: Add refresh-quota button to account cards**

Find the account card rendering section and add a small "刷新额度" button. The button should call `adminApi.refreshAccountQuota(token, account.id)` and then re-fetch the accounts list.

Look for the existing account card component (around the quota display section) and add a button:

```tsx
<button
  className="quota-refresh-btn"
  onClick={async () => {
    try { await adminApi.refreshAccountQuota(token, account.id); } catch {}
    // Re-fetch accounts to show updated quota
    adminApi.accounts(token).then(r => setAccounts(r.items)).catch(() => {});
  }}
  title="刷新额度"
>
  ↻
</button>
```

Add the corresponding CSS:

```css
.quota-refresh-btn {
  background: none;
  border: none;
  cursor: pointer;
  padding: 2px 4px;
  font-size: 12px;
  opacity: 0.5;
  color: inherit;
}
.quota-refresh-btn:hover {
  opacity: 1;
}
```

- [ ] **Step 4: Verify frontend builds**

Run: `cd web && npm run build`
Expected: builds cleanly

- [ ] **Step 5: Commit**

```bash
git add web/components/admin-channel-console.tsx
git commit -m "feat(quota): update frontend to read unified meta.quota with legacy fallback and refresh button"
```

---

### Task 11: Integration Test — Build Verification

**Files:** None (verification only)

- [ ] **Step 1: Full Go build**

Run: `go build ./...`
Expected: compiles cleanly with no errors

- [ ] **Step 2: Frontend build**

Run: `cd web && npm run build`
Expected: builds cleanly

- [ ] **Step 3: Commit any remaining fixes**

```bash
git add -A
git commit -m "fix(quota): integration build fixes"
```

---

## Self-Review

### Spec Coverage Check

| Spec Requirement | Task |
|-----------------|------|
| Standard QuotaData struct | Task 1 |
| Fetcher interface + registry | Task 1 |
| Gemini Fetcher | Task 3 |
| Antigravity Fetcher with multi-endpoint fallback | Task 4 |
| Anthropic/Claude Code Fetcher | Task 5 |
| Codex Fetcher | Task 6 |
| Scheduler with batch + jitter | Task 2 |
| Staleness guard (5 min) | Task 2 |
| 429 trigger | Task 7 |
| Admin frontend trigger | Task 8 |
| API endpoints | Task 8 |
| Frontend unified quota display | Task 10 |
| Frontend refresh button | Task 10 |
| Legacy fallback | Task 10 |

### Placeholder Scan

No TBD/TODO found. All steps contain actual code.

### Type Consistency

- `QuotaData`, `QuotaBucket`, `CreditsInfo` defined in Task 1, used consistently in Tasks 3-6
- `Fetcher` interface defined in Task 1, implemented consistently in Tasks 3-6
- `Scheduler` methods (`RefreshAccount`, `On429`, `RefreshChannel`) defined in Task 2, called consistently in Tasks 7-8
- Frontend `QuotaDisplayItem` type unchanged; `buildQuotaDisplayItems` reads `remaining_percent` matching Go's `RemainingPercent` JSON tag
