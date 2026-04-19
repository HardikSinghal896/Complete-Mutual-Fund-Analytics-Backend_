package handler

import (
	"sync"
	"time"
)

const cacheTTL = 60 * time.Second

type cacheEntry struct {
	data   []byte
	expiry time.Time
}

// ResponseCache is a simple TTL map for JSON response bytes.
// Stored as raw []byte so cached responses are written to the wire without
// re-encoding. Thread-safe via RWMutex.
type ResponseCache struct {
	mu    sync.RWMutex
	store map[string]cacheEntry
}

func NewResponseCache() *ResponseCache {
	return &ResponseCache{store: make(map[string]cacheEntry)}
}

// Get returns the cached bytes and true if the entry exists and has not expired.
func (c *ResponseCache) Get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.store[key]
	if !ok || time.Now().After(e.expiry) {
		return nil, false
	}
	return e.data, true
}

// Set stores bytes under key with a fresh TTL.
func (c *ResponseCache) Set(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = cacheEntry{data: data, expiry: time.Now().Add(cacheTTL)}
}

// Flush discards all cached entries. Called after a background data refresh.
func (c *ResponseCache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store = make(map[string]cacheEntry)
}
