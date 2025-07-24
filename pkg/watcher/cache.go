package watcher

import (
	"context"
	"sync"
	"time"
)

// Cache is a generic thread-safe TTL cache
type Cache[T any] struct {
	mu        sync.RWMutex
	ttl       time.Duration
	timestamp time.Time
	value     T
	ok        bool // Indicates whether value has been set at least once
}

// NewCache creates a new TTL cache
func NewCache[T any](ttl time.Duration) *Cache[T] {
	return &Cache[T]{
		ttl: ttl,
	}
}

// GetOrUpdate returns the cached value if it's fresh, or calls fetch() to refresh it.
func (c *Cache[T]) GetOrUpdate(ctx context.Context, fetch func(context.Context) (T, error)) (T, error) {
	c.mu.RLock()
	if c.ok && time.Since(c.timestamp) < c.ttl {
		val := c.value
		c.mu.RUnlock()
		return val, nil
	}
	c.mu.RUnlock()

	// Cache is stale, get write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check to avoid duplicate fetch in case another goroutine refreshed it
	if c.ok && time.Since(c.timestamp) < c.ttl {
		return c.value, nil
	}

	// Call the fetcher
	val, err := fetch(ctx)
	if err != nil {
		var zero T
		return zero, err
	}

	c.value = val
	c.timestamp = time.Now()
	c.ok = true

	return val, nil
}
