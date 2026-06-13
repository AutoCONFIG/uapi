package relay

import (
	"strings"
	"sync"
	"time"

	"github.com/AutoCONFIG/uapi/internal/db"
	"github.com/google/uuid"
)

type WeightedAccount struct {
	Account        *db.Account
	Weight         int
	CurrentWeight  int
	OriginalWeight int
	InFlight       int
}

type AccountPool struct {
	mu          sync.RWMutex
	accounts    []WeightedAccount
	totalWeight int
	closed      bool // set by Close to prevent cooldown goroutines from acting on removed pools
}

type AccountPoolStats struct {
	Accounts      int  `json:"accounts"`
	TotalWeight   int  `json:"total_weight"`
	TotalInFlight int  `json:"total_in_flight"`
	Closed        bool `json:"closed"`
}

const accountInFlightSoftCap = 10

func NewAccountPool(accounts []*db.Account) *AccountPool {
	p := &AccountPool{}
	wa := make([]WeightedAccount, len(accounts))
	total := 0
	cooldowns := make(map[string]time.Time)
	for i, acc := range accounts {
		w := acc.Weight
		if !acc.Enabled {
			w = 0
		}
		if acc.CooldownUntil != nil && time.Now().Before(*acc.CooldownUntil) {
			w = 0
			cooldowns[acc.ID.String()] = *acc.CooldownUntil
		}
		wa[i] = WeightedAccount{
			Account:        acc,
			Weight:         w,
			CurrentWeight:  0,
			OriginalWeight: acc.Weight,
		}
		total += w
	}
	p.accounts = wa
	p.totalWeight = total
	p.restoreCooldowns(cooldowns)
	return p
}

// Pick selects an account using smooth weighted round-robin.
func (p *AccountPool) Pick() (*db.Account, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.pickLocked(nil, "")
}

func (p *AccountPool) PickExcluding(excluded map[string]bool) (*db.Account, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pickLocked(excluded, "")
}

// PickForModel selects an account with quota available for the requested model.
// For per-model quota channels (Gemini, Antigravity), it skips accounts where
// the matching quota bucket is exhausted (remaining <= 0).
func (p *AccountPool) PickForModel(model string, excluded map[string]bool) (*db.Account, bool) {
	if model == "" {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.pickLocked(excluded, "")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.pickLocked(excluded, model)
}

func (p *AccountPool) PickForModelWithSessionLoad(model string, excluded map[string]bool, sessionLoad map[string]int) (*db.Account, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	acc, load, ok := p.pickWithSessionLoadLocked(excluded, model, sessionLoad)
	return acc, load, ok
}

// modelQuotaExhausted checks if the account's quota for the given model is exhausted.
// It looks for a quota bucket whose label matches the model name and checks if remaining <= 0.
func modelQuotaExhausted(acc *db.Account, model string) bool {
	if acc.Metadata == nil {
		return false
	}
	quotaRaw, ok := acc.Metadata["quota"]
	if !ok || quotaRaw == nil {
		return false
	}
	// quota is stored as *QuotaData or map[string]interface{} after JSON round-trip
	quotaData, ok := quotaRaw.(interface{})
	if !ok {
		return false
	}

	lowerModel := strings.ToLower(model)
	buckets := extractQuotaBuckets(quotaData)
	if len(buckets) == 0 {
		return false
	}
	if codexQuotaExhausted(acc, buckets) {
		return true
	}

	hasMatchingBucket := false
	for _, b := range buckets {
		lowerLabel := strings.ToLower(b.label)
		if strings.Contains(lowerModel, "pro") && strings.Contains(lowerLabel, "pro") {
			hasMatchingBucket = true
			if b.remaining <= 0 {
				return true
			}
		} else if strings.Contains(lowerModel, "flash") && strings.Contains(lowerLabel, "flash") {
			hasMatchingBucket = true
			if b.remaining <= 0 {
				return true
			}
		} else if strings.Contains(lowerModel, "lite") && strings.Contains(lowerLabel, "lite") {
			hasMatchingBucket = true
			if b.remaining <= 0 {
				return true
			}
		} else if strings.Contains(lowerLabel, lowerModel) || strings.Contains(lowerModel, lowerLabel) {
			hasMatchingBucket = true
			if b.remaining <= 0 {
				return true
			}
		}
	}
	_ = hasMatchingBucket
	return false
}

func codexQuotaExhausted(acc *db.Account, buckets []modelQuotaBucket) bool {
	if !isCodexQuotaAccount(acc) {
		return false
	}
	for _, b := range buckets {
		if !strings.Contains(strings.ToLower(b.label), "codex") {
			continue
		}
		if b.remaining <= 0 {
			return true
		}
	}
	return false
}

func codexQuotaSkipDebug(acc *db.Account) map[string]interface{} {
	if acc == nil || acc.Metadata == nil || !isCodexQuotaAccount(acc) {
		return nil
	}
	buckets := extractQuotaBuckets(acc.Metadata["quota"])
	if len(buckets) == 0 {
		return nil
	}
	exhausted := make([]map[string]interface{}, 0)
	for _, b := range buckets {
		if !strings.Contains(strings.ToLower(b.label), "codex") || b.remaining > 0 {
			continue
		}
		exhausted = append(exhausted, map[string]interface{}{
			"label":             b.label,
			"remaining_percent": b.remaining,
		})
	}
	if len(exhausted) == 0 {
		return nil
	}
	item := map[string]interface{}{
		"account_id":        acc.ID.String(),
		"account_name":      acc.Name,
		"reason":            "codex_quota_exhausted",
		"exhausted_buckets": exhausted,
	}
	if value, ok := acc.Metadata["chatgpt_account_id"].(string); ok && strings.TrimSpace(value) != "" {
		item["chatgpt_account_id"] = strings.TrimSpace(value)
	}
	if value, ok := acc.Metadata["chatgpt_plan_type"].(string); ok && strings.TrimSpace(value) != "" {
		item["chatgpt_plan_type"] = strings.TrimSpace(value)
	}
	return item
}

func isCodexQuotaAccount(acc *db.Account) bool {
	if acc == nil || acc.Metadata == nil {
		return false
	}
	for _, key := range []string{"oauth_provider", "auth_mode"} {
		if value, ok := acc.Metadata[key].(string); ok && strings.EqualFold(strings.TrimSpace(value), "codex") {
			return true
		}
	}
	if value, ok := acc.Metadata["auth_mode"].(string); ok && strings.EqualFold(strings.TrimSpace(value), "chatgpt") {
		if _, hasPlan := acc.Metadata["chatgpt_plan_type"]; hasPlan {
			return true
		}
	}
	if _, ok := acc.Metadata["chatgpt_account_id"]; ok {
		return true
	}
	return false
}

type modelQuotaBucket struct {
	label     string
	remaining int
}

func extractQuotaBuckets(quotaRaw interface{}) []modelQuotaBucket {
	switch q := quotaRaw.(type) {
	case map[string]interface{}:
		if buckets, ok := q["buckets"].([]interface{}); ok {
			return parseBucketsFromSlice(buckets)
		}
	case map[string]map[string]interface{}:
		// Already deserialized as typed map
	}
	// Try via JSON round-trip for *QuotaData stored as struct
	if data, ok := quotaRaw.(interface{ GetBuckets() interface{} }); ok {
		_ = data
	}
	return nil
}

func parseBucketsFromSlice(buckets []interface{}) []modelQuotaBucket {
	var result []modelQuotaBucket
	for _, b := range buckets {
		bm, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		label, _ := bm["label"].(string)
		remaining := 0
		if rp, ok := bm["remaining_percent"].(float64); ok {
			remaining = int(rp)
		} else if rp, ok := bm["RemainingPercent"].(float64); ok {
			remaining = int(rp)
		} else if rp, ok := bm["remaining_percent"].(int); ok {
			remaining = rp
		}
		if label != "" {
			result = append(result, modelQuotaBucket{label: label, remaining: remaining})
		}
	}
	return result
}

func (p *AccountPool) pickLocked(excluded map[string]bool, model string) (*db.Account, bool) {
	preferBelowSoftCap := false
	for i := range p.accounts {
		if !p.accountSelectableLocked(i, excluded, model) {
			continue
		}
		if p.accounts[i].InFlight < accountInFlightSoftCap {
			preferBelowSoftCap = true
			break
		}
	}

	totalWeight := 0
	for i := range p.accounts {
		if !p.accountSelectableLocked(i, excluded, model) {
			continue
		}
		if preferBelowSoftCap && p.accounts[i].InFlight >= accountInFlightSoftCap {
			continue
		}
		totalWeight += p.effectiveAccountWeightLocked(i)
	}
	if totalWeight == 0 {
		return nil, false
	}

	var best *WeightedAccount
	for i := range p.accounts {
		if !p.accountSelectableLocked(i, excluded, model) {
			continue
		}
		if preferBelowSoftCap && p.accounts[i].InFlight >= accountInFlightSoftCap {
			continue
		}
		effectiveWeight := p.effectiveAccountWeightLocked(i)
		p.accounts[i].CurrentWeight += effectiveWeight
		if best == nil ||
			p.accounts[i].CurrentWeight > best.CurrentWeight ||
			(p.accounts[i].CurrentWeight == best.CurrentWeight && p.accounts[i].InFlight < best.InFlight) {
			best = &p.accounts[i]
		}
	}
	if best == nil {
		return nil, false
	}
	best.CurrentWeight -= totalWeight
	return best.Account, true
}

func (p *AccountPool) pickWithSessionLoadLocked(excluded map[string]bool, model string, sessionLoad map[string]int) (*db.Account, int, bool) {
	preferBelowSoftCap := false
	minSessions := 0
	haveCandidate := false
	for i := range p.accounts {
		if !p.accountSelectableLocked(i, excluded, model) {
			continue
		}
		if p.accounts[i].InFlight < accountInFlightSoftCap {
			preferBelowSoftCap = true
		}
		load := p.accountSessionLoadLocked(i, sessionLoad)
		if !haveCandidate || load < minSessions {
			minSessions = load
			haveCandidate = true
		}
	}
	if !haveCandidate {
		return nil, 0, false
	}
	haveCandidate = false
	for i := range p.accounts {
		if !p.accountSelectableLocked(i, excluded, model) {
			continue
		}
		if preferBelowSoftCap && p.accounts[i].InFlight >= accountInFlightSoftCap {
			continue
		}
		load := p.accountSessionLoadLocked(i, sessionLoad)
		if !haveCandidate || load < minSessions {
			minSessions = load
			haveCandidate = true
		}
	}
	if !haveCandidate {
		return nil, 0, false
	}

	totalWeight := 0
	for i := range p.accounts {
		if !p.accountSelectableLocked(i, excluded, model) {
			continue
		}
		if preferBelowSoftCap && p.accounts[i].InFlight >= accountInFlightSoftCap {
			continue
		}
		if p.accountSessionLoadLocked(i, sessionLoad) != minSessions {
			continue
		}
		totalWeight += p.effectiveAccountWeightLocked(i)
	}
	if totalWeight == 0 {
		return nil, 0, false
	}

	var best *WeightedAccount
	for i := range p.accounts {
		if !p.accountSelectableLocked(i, excluded, model) {
			continue
		}
		if preferBelowSoftCap && p.accounts[i].InFlight >= accountInFlightSoftCap {
			continue
		}
		if p.accountSessionLoadLocked(i, sessionLoad) != minSessions {
			continue
		}
		effectiveWeight := p.effectiveAccountWeightLocked(i)
		p.accounts[i].CurrentWeight += effectiveWeight
		if best == nil ||
			p.accounts[i].CurrentWeight > best.CurrentWeight ||
			(p.accounts[i].CurrentWeight == best.CurrentWeight && p.accounts[i].InFlight < best.InFlight) {
			best = &p.accounts[i]
		}
	}
	if best == nil {
		return nil, 0, false
	}
	best.CurrentWeight -= totalWeight
	return best.Account, minSessions, true
}

func (p *AccountPool) accountSessionLoadLocked(i int, sessionLoad map[string]int) int {
	if sessionLoad == nil || p.accounts[i].Account == nil {
		return 0
	}
	return sessionLoad[p.accounts[i].Account.ID.String()]
}

func (p *AccountPool) accountSelectableLocked(i int, excluded map[string]bool, model string) bool {
	if p.accounts[i].Weight <= 0 {
		return false
	}
	acc := p.accounts[i].Account
	if acc == nil {
		return false
	}
	if excluded != nil && excluded[acc.ID.String()] {
		return false
	}
	return model == "" || !modelQuotaExhausted(acc, model)
}

func (p *AccountPool) effectiveAccountWeightLocked(i int) int {
	weight := p.accounts[i].Weight
	if weight <= 0 {
		return 0
	}
	load := p.accounts[i].InFlight + 1
	penalized := weight / (load * load)
	if penalized < 1 {
		return 1
	}
	return penalized
}

func (p *AccountPool) Begin(accountID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].Account == nil || p.accounts[i].Account.ID.String() != accountID {
			continue
		}
		p.accounts[i].InFlight++
		return p.accounts[i].InFlight
	}
	return 0
}

func (p *AccountPool) End(accountID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].Account == nil || p.accounts[i].Account.ID.String() != accountID {
			continue
		}
		if p.accounts[i].InFlight > 0 {
			p.accounts[i].InFlight--
		}
		return p.accounts[i].InFlight
	}
	return 0
}

func (p *AccountPool) InFlight(accountID string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := range p.accounts {
		if p.accounts[i].Account != nil && p.accounts[i].Account.ID.String() == accountID {
			return p.accounts[i].InFlight
		}
	}
	return 0
}

func (p *AccountPool) HasAvailableBelowSoftCapForModel(model string, excluded map[string]bool) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := range p.accounts {
		if !p.accountSelectableLocked(i, excluded, model) {
			continue
		}
		if p.accounts[i].InFlight < accountInFlightSoftCap {
			return true
		}
	}
	return false
}

func (p *AccountPool) totalInFlightLocked() int {
	total := 0
	for i := range p.accounts {
		total += p.accounts[i].InFlight
	}
	return total
}

func (p *AccountPool) snapshotInFlight() map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]int, len(p.accounts))
	for i := range p.accounts {
		if p.accounts[i].Account == nil || p.accounts[i].InFlight <= 0 {
			continue
		}
		out[p.accounts[i].Account.ID.String()] = p.accounts[i].InFlight
	}
	return out
}

func (p *AccountPool) snapshotCooldowns(now time.Time) map[string]time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]time.Time, len(p.accounts))
	for i := range p.accounts {
		if p.accounts[i].Account == nil || p.accounts[i].Account.CooldownUntil == nil {
			continue
		}
		if !p.accounts[i].Account.CooldownUntil.After(now) {
			continue
		}
		out[p.accounts[i].Account.ID.String()] = *p.accounts[i].Account.CooldownUntil
	}
	return out
}

func (p *AccountPool) restoreInFlight(inFlight map[string]int) {
	if len(inFlight) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].Account == nil {
			continue
		}
		p.accounts[i].InFlight = inFlight[p.accounts[i].Account.ID.String()]
	}
}

func (p *AccountPool) restoreCooldowns(cooldowns map[string]time.Time) {
	for accountID, until := range cooldowns {
		p.CooldownUntil(accountID, until)
	}
}

func (p *AccountPool) PickByID(accountID string) (*db.Account, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.totalWeight == 0 {
		return nil, false
	}
	for i := range p.accounts {
		if p.accounts[i].Account.ID.String() != accountID || p.accounts[i].Weight <= 0 {
			continue
		}
		return p.accounts[i].Account, true
	}
	return nil, false
}

// PickByIDForModel selects a specific account by ID with model quota check.
// Unlike PickByID (which only checks Weight), this also verifies model-specific
// quota availability, ensuring the affinity-cached account is still viable.
func (p *AccountPool) PickByIDForModel(accountID string, model string) (*db.Account, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.totalWeight == 0 {
		return nil, false
	}
	for i := range p.accounts {
		if p.accounts[i].Account.ID.String() != accountID {
			continue
		}
		// Weight <= 0 covers: cooldown active, disabled, or exhausted
		if p.accounts[i].Weight <= 0 {
			return nil, false
		}
		// Model-specific quota check
		if model != "" && modelQuotaExhausted(p.accounts[i].Account, model) {
			return nil, false
		}
		return p.accounts[i].Account, true
	}
	return nil, false
}

func (p *AccountPool) QuotaSkipDebugForModel(model string, excluded map[string]bool) []map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]map[string]interface{}, 0)
	for i := range p.accounts {
		acc := p.accounts[i].Account
		if acc == nil || p.accounts[i].Weight <= 0 {
			continue
		}
		if excluded != nil && excluded[acc.ID.String()] {
			continue
		}
		if item := codexQuotaSkipDebug(acc); item != nil {
			out = append(out, item)
		}
	}
	return out
}

func (p *AccountPool) QuotaSkipDebugForAccount(accountID string) []map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := range p.accounts {
		acc := p.accounts[i].Account
		if acc == nil || acc.ID.String() != accountID {
			continue
		}
		if item := codexQuotaSkipDebug(acc); item != nil {
			return []map[string]interface{}{item}
		}
		return nil
	}
	return nil
}

func (p *AccountPool) AvailableCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	count := 0
	for i := range p.accounts {
		if p.accounts[i].Weight > 0 {
			count++
		}
	}
	return count
}

func (p *AccountPool) Stats() AccountPoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return AccountPoolStats{Accounts: len(p.accounts), TotalWeight: p.totalWeight, TotalInFlight: p.totalInFlightLocked(), Closed: p.closed}
}

func (p *AccountPool) Cooldown(accountID string, duration time.Duration) {
	p.CooldownUntil(accountID, time.Now().Add(duration))
}

func (p *AccountPool) CooldownUntil(accountID string, until time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].Account.ID.String() == accountID {
			if current := p.accounts[i].Account.CooldownUntil; current != nil && current.After(until) {
				until = *current
			}
			if p.accounts[i].Weight > 0 {
				p.totalWeight -= p.accounts[i].Weight
				p.accounts[i].Weight = 0
				p.accounts[i].CurrentWeight = 0
			}
			p.accounts[i].Account.CooldownUntil = &until
			cooldownID := p.accounts[i].Account.ID.String()
			cooldownWeight := p.accounts[i].OriginalWeight
			duration := time.Until(until)
			if duration < 0 {
				duration = 0
			}
			time.AfterFunc(duration, func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				if p.closed {
					return // pool has been removed, skip cooldown restore
				}
				for j := range p.accounts {
					if p.accounts[j].Account.ID.String() == cooldownID {
						if until := p.accounts[j].Account.CooldownUntil; until != nil && time.Now().Before(*until) {
							return
						}
						if !p.accounts[j].Account.Enabled || p.accounts[j].Weight > 0 {
							return
						}
						p.accounts[j].Account.CooldownUntil = nil
						p.accounts[j].Weight = cooldownWeight
						p.totalWeight += cooldownWeight
						break
					}
				}
			})
			return
		}
	}
}

func (p *AccountPool) Disable(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].Account.ID.String() != accountID {
			continue
		}
		if p.accounts[i].Weight > 0 {
			p.totalWeight -= p.accounts[i].Weight
		}
		p.accounts[i].Weight = 0
		p.accounts[i].CurrentWeight = 0
		p.accounts[i].Account.Enabled = false
		return
	}
}

func (p *AccountPool) RestoreAccount(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].Account.ID.String() != accountID {
			continue
		}
		p.accounts[i].Account.Enabled = true
		p.accounts[i].Account.CooldownUntil = nil
		if p.accounts[i].Weight <= 0 && p.accounts[i].OriginalWeight > 0 {
			p.accounts[i].Weight = p.accounts[i].OriginalWeight
			p.totalWeight += p.accounts[i].Weight
		}
		return
	}
}

// PoolManager manages all channel pools.
type PoolManager struct {
	mu    sync.RWMutex
	pools map[string]*AccountPool // channel_id -> pool
}

func NewPoolManager() *PoolManager {
	return &PoolManager{
		pools: make(map[string]*AccountPool),
	}
}

func (pm *PoolManager) SetPool(channelID string, pool *AccountPool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if existing, ok := pm.pools[channelID]; ok && existing != nil && pool != nil {
		pool.restoreInFlight(existing.snapshotInFlight())
		pool.restoreCooldowns(existing.snapshotCooldowns(time.Now()))
		existing.Close()
	}
	pm.pools[channelID] = pool
}

func (pm *PoolManager) GetPool(channelID string) (*AccountPool, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	p, ok := pm.pools[channelID]
	return p, ok
}

func (pm *PoolManager) RemovePool(channelID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if p, ok := pm.pools[channelID]; ok {
		p.Close()
	}
	delete(pm.pools, channelID)
}

func (pm *PoolManager) Snapshot() map[uuid.UUID]*AccountPool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make(map[uuid.UUID]*AccountPool, len(pm.pools))
	for channelID, pool := range pm.pools {
		if id, err := uuid.Parse(channelID); err == nil {
			out[id] = pool
		}
	}
	return out
}

// Close marks the pool as closed so pending cooldown goroutines will no-op.
func (p *AccountPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}
