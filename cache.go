package evcache

import (
	"runtime"
	"time"

	"github.com/mgnsk/evcache/v4/internal/backend"
)

// Cache is an in-memory cache.
type Cache[K comparable, V any] struct {
	backend *backend.Backend[K, V]
}

// New creates a new empty cache.
func New[K comparable, V any](opt ...Option) *Cache[K, V] {
	opts := newDefaultCacheOptions()

	for _, o := range opt {
		o.apply(&opts)
	}

	be := &backend.Backend[K, V]{}
	be.Init(opts.capacity, opts.policy, opts.ttl, opts.debounce)

	c := &Cache[K, V]{
		backend: be,
	}

	runtime.SetFinalizer(c, func(c *Cache[K, V]) {
		c.backend.Close()
	})

	return c
}

// Len returns the number of initialized values.
func (c *Cache[K, V]) Len() int {
	return c.backend.Len()
}

// Has returns whether the value for key is initialized.
func (c *Cache[K, V]) Has(key K) bool {
	return c.backend.Has(key)
}

// Load an initialized value.
func (c *Cache[K, V]) Load(key K) (value V, loaded bool) {
	return c.backend.Load(key)
}

// Range iterates over initialized cache key-value pairs in no particular order or consistency.
// If f returns false, range stops the iteration.
//
// f may modify the cache.
func (c *Cache[K, V]) Range(f func(key K, value V) bool) {
	c.backend.Range(f)
}

// Evict a value.
func (c *Cache[K, V]) Evict(key K) (value V, evicted bool) {
	return c.backend.Evict(key)
}

// Store a value with the default TTL.
func (c *Cache[K, V]) Store(key K, value V) {
	c.backend.Store(key, value)
}

// StoreTTL stores a value with specified TTL.
func (c *Cache[K, V]) StoreTTL(key K, value V, ttl time.Duration) {
	c.backend.StoreTTL(key, value, ttl)
}

// Fetch loads or stores a value for key with the default TTL.
// If a value exists, f will not be called, otherwise f will be called to fetch the new value.
// It panics if f panics. Concurrent fetches for the same key will block and return a single result.
func (c *Cache[K, V]) Fetch(key K, f func() (V, error)) (value V, err error) {
	return c.backend.Fetch(key, f)
}

// FetchTTL loads or stores a value for key with the specified TTL.
// If a value exists, f will not be called, otherwise f will be called to fetch the new value.
// It panics if f panics. Concurrent fetches for the same key will block and return a single result.
func (c *Cache[K, V]) FetchTTL(key K, f func() (V, time.Duration, error)) (value V, err error) {
	return c.backend.FetchTTL(key, f)
}
