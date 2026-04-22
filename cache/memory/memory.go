// Package memory provides an in-memory cache backend for testing
// and as a fallback when Redis is unavailable.
package memory

import (
	"sync"
	"time"
)

// entry holds a cached value with its expiration deadline.
type entry struct {
	value   []byte
	expires time.Time
}

// Cache is an in-memory cache implementation using sync.Map.
type Cache struct {
	mu       sync.RWMutex
	data     map[string]*entry
	maxKeys  int
	stopGC   chan struct{}
	gcTicker *time.Ticker
}

// New constructs an in-memory cache with the given maximum key limit.
// If maxKeys is 0, no limit is enforced.
func New(maxKeys int) *Cache {
	c := &Cache{
		data:    make(map[string]*entry),
		maxKeys: maxKeys,
		stopGC:  make(chan struct{}),
	}
	c.startGC()
	return c
}

// Store stores a frame value under the given key with the specified TTL.
func (c *Cache) Store(key []byte, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	k := string(key)
	if c.maxKeys > 0 && len(c.data) >= c.maxKeys {
		// Simple eviction: remove oldest entry (first in map iteration)
		for keyToRemove := range c.data {
			delete(c.data, keyToRemove)
			break
		}
	}

	c.data[k] = &entry{
		value:   value,
		expires: time.Now().Add(ttl),
	}
	return nil
}

// Retrieve retrieves the frame value for the given key.
// Returns nil if the key does not exist or has expired.
func (c *Cache) Retrieve(key []byte) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	e, ok := c.data[string(key)]
	if !ok {
		return nil, nil
	}

	if time.Now().After(e.expires) {
		// Expired entry
		return nil, nil
	}

	return e.value, nil
}

// Delete removes the key from the cache.
func (c *Cache) Delete(key []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.data, string(key))
	return nil
}

// Close releases resources and stops the background GC.
func (c *Cache) Close() error {
	c.stopGC <- struct{}{}
	c.gcTicker.Stop()
	return nil
}

// startGC runs a background goroutine to periodically sweep expired entries.
func (c *Cache) startGC() {
	c.gcTicker = time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-c.gcTicker.C:
				c.sweepExpired()
			case <-c.stopGC:
				return
			}
		}
	}()
}

// sweepExpired removes all expired entries from the cache.
func (c *Cache) sweepExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for k, e := range c.data {
		if now.After(e.expires) {
			delete(c.data, k)
		}
	}
}
