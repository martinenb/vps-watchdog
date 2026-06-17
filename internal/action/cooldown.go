package action

import (
	"sync"
	"time"
)

// CooldownRegistry tracks when an action was last taken so we don't spam alerts.
type CooldownRegistry struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

// NewCooldownRegistry returns an initialised registry.
func NewCooldownRegistry() *CooldownRegistry {
	return &CooldownRegistry{entries: make(map[string]time.Time)}
}

// Allow returns true if the key has not been used within the given duration.
// It records the current time if it returns true.
func (r *CooldownRegistry) Allow(key string, duration time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	last, ok := r.entries[key]
	if ok && time.Since(last) < duration {
		return false
	}
	r.entries[key] = time.Now()
	return true
}

// Reset clears the cooldown for a key, allowing the next call to Allow to succeed immediately.
func (r *CooldownRegistry) Reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, key)
}
