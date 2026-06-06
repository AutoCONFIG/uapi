package relay

import (
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
}

type AccountPool struct {
	mu          sync.RWMutex
	accounts    []WeightedAccount
	totalWeight int
	closed      bool // set by Close to prevent cooldown goroutines from acting on removed pools
}

type AccountPoolStats struct {
	Accounts    int  `json:"accounts"`
	TotalWeight int  `json:"total_weight"`
	Closed      bool `json:"closed"`
}

func NewAccountPool(accounts []*db.Account) *AccountPool {
	p := &AccountPool{}
	wa := make([]WeightedAccount, len(accounts))
	total := 0
	for i, acc := range accounts {
		w := acc.Weight
		if !acc.Enabled {
			w = 0
		}
		if acc.CooldownUntil != nil && time.Now().Before(*acc.CooldownUntil) {
			w = 0
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
	return p
}

// Pick selects an account using smooth weighted round-robin.
func (p *AccountPool) Pick() (*db.Account, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.totalWeight == 0 {
		return nil, false
	}

	var best *WeightedAccount
	for i := range p.accounts {
		p.accounts[i].CurrentWeight += p.accounts[i].Weight
		if best == nil || p.accounts[i].CurrentWeight > best.CurrentWeight {
			best = &p.accounts[i]
		}
	}
	if best != nil {
		best.CurrentWeight -= p.totalWeight
		return best.Account, true
	}
	return nil, false
}

func (p *AccountPool) Stats() AccountPoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return AccountPoolStats{Accounts: len(p.accounts), TotalWeight: p.totalWeight, Closed: p.closed}
}

func (p *AccountPool) Cooldown(accountID string, duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].Account.ID.String() == accountID {
			if p.accounts[i].Weight == 0 {
				return // already cooled down, skip
			}
			p.totalWeight -= p.accounts[i].Weight
			p.accounts[i].Weight = 0
			p.accounts[i].CurrentWeight = 0
			until := time.Now().Add(duration)
			p.accounts[i].Account.CooldownUntil = &until
			cooldownID := p.accounts[i].Account.ID.String()
			cooldownWeight := p.accounts[i].OriginalWeight
			time.AfterFunc(duration, func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				if p.closed {
					return // pool has been removed, skip cooldown restore
				}
				for j := range p.accounts {
					if p.accounts[j].Account.ID.String() == cooldownID {
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
