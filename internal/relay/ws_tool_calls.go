package relay

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

type wsToolCallCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[string]wsToolCallEntry
}

type wsToolCallEntry struct {
	sessionID string
	key       string
	item      json.RawMessage
	lastUsed  time.Time
}

func newWSToolCallCache(ttl time.Duration, max int) *wsToolCallCache {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if max <= 0 {
		max = 2048
	}
	return &wsToolCallCache{
		ttl:     ttl,
		max:     max,
		entries: make(map[string]wsToolCallEntry),
	}
}

func (c *wsToolCallCache) RepairCreate(sessionID string, payload []byte) []byte {
	if c == nil || sessionID == "" || len(payload) == 0 {
		return payload
	}

	var root map[string]interface{}
	if err := json.Unmarshal(payload, &root); err != nil {
		return payload
	}
	input, ok := root["input"].([]interface{})
	if !ok || len(input) == 0 {
		c.RecordCreate(sessionID, payload)
		return payload
	}

	now := time.Now()
	seenCalls := make(map[string]bool)
	var repaired []interface{}
	changed := false

	c.mu.Lock()
	c.pruneLocked(now)
	for _, rawItem := range input {
		item, _ := rawItem.(map[string]interface{})
		itemType, _ := item["type"].(string)
		callID, _ := item["call_id"].(string)

		if key := wsToolCallKeyForCall(itemType, callID); key != "" {
			seenCalls[key] = true
			c.recordItemLocked(sessionID, key, item, now)
		}

		if key := wsToolCallKeyForOutput(itemType, callID); key != "" && !seenCalls[key] {
			if entry, ok := c.entries[c.entryKey(sessionID, key)]; ok && len(entry.item) > 0 {
				var cached interface{}
				if err := json.Unmarshal(entry.item, &cached); err == nil {
					repaired = append(repaired, cached)
					seenCalls[key] = true
					changed = true
					entry.lastUsed = now
					c.entries[c.entryKey(sessionID, key)] = entry
				}
			}
		}

		repaired = append(repaired, rawItem)
	}
	c.mu.Unlock()

	if !changed {
		return payload
	}
	root["input"] = repaired
	encoded, err := json.Marshal(root)
	if err != nil {
		return payload
	}
	return encoded
}

func (c *wsToolCallCache) RecordCreate(sessionID string, payload []byte) {
	if c == nil || sessionID == "" || len(payload) == 0 {
		return
	}
	var root struct {
		Input []map[string]interface{} `json:"input"`
	}
	if err := json.Unmarshal(payload, &root); err != nil {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	for _, item := range root.Input {
		itemType, _ := item["type"].(string)
		callID, _ := item["call_id"].(string)
		if key := wsToolCallKeyForCall(itemType, callID); key != "" {
			c.recordItemLocked(sessionID, key, item, now)
		}
	}
}

func (c *wsToolCallCache) RecordResponseEvent(sessionID string, payload []byte) {
	if c == nil || sessionID == "" || len(payload) == 0 {
		return
	}
	var ev struct {
		Type string                 `json:"type"`
		Item map[string]interface{} `json:"item"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return
	}
	if ev.Type != WSEventOutputItemAdded && ev.Type != WSEventOutputItemDone {
		return
	}
	itemType, _ := ev.Item["type"].(string)
	callID, _ := ev.Item["call_id"].(string)
	key := wsToolCallKeyForCall(itemType, callID)
	if key == "" {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	c.recordItemLocked(sessionID, key, ev.Item, now)
}

func (c *wsToolCallCache) DeleteSession(sessionID string) {
	if c == nil || sessionID == "" {
		return
	}
	prefix := sessionID + "\x00"
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
}

func (c *wsToolCallCache) recordItemLocked(sessionID, key string, item map[string]interface{}, now time.Time) {
	encoded, err := json.Marshal(item)
	if err != nil {
		return
	}
	c.entries[c.entryKey(sessionID, key)] = wsToolCallEntry{
		sessionID: sessionID,
		key:       key,
		item:      append(json.RawMessage(nil), encoded...),
		lastUsed:  now,
	}
	if len(c.entries) > c.max {
		c.evictOldestLocked()
	}
}

func (c *wsToolCallCache) pruneLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.Sub(entry.lastUsed) >= c.ttl {
			delete(c.entries, key)
		}
	}
	for len(c.entries) > c.max {
		c.evictOldestLocked()
	}
}

func (c *wsToolCallCache) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for key, entry := range c.entries {
		if oldestKey == "" || entry.lastUsed.Before(oldest) {
			oldestKey = key
			oldest = entry.lastUsed
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func (c *wsToolCallCache) entryKey(sessionID, key string) string {
	return sessionID + "\x00" + key
}

func wsToolCallKeyForCall(itemType, callID string) string {
	if callID == "" {
		return ""
	}
	switch itemType {
	case "function_call", "custom_tool_call":
		return itemType + "\x00" + callID
	default:
		return ""
	}
}

func wsToolCallKeyForOutput(itemType, callID string) string {
	if callID == "" {
		return ""
	}
	switch itemType {
	case "function_call_output":
		return "function_call\x00" + callID
	case "custom_tool_call_output":
		return "custom_tool_call\x00" + callID
	default:
		return ""
	}
}
