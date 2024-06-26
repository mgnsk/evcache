/*
Package evcache implements a concurrent key-value cache with capacity overflow eviction, item expiry and deduplication.
*/
package evcache

import (
	"runtime"
	"time"

	"github.com/mgnsk/evcache/v3/internal/backend"
	"github.com/mgnsk/ringlist"
)

// Cache is an in-memory TTL cache with optional capacity.
type Cache[K comparable, V any] struct {
	backend *backend.Backend[K, V]
}

// New creates an empty cache.
func New[K comparable, V any](capacity int) *Cache[K, V] {
	c := &Cache[K, V]{
		backend: backend.NewBackend[K, V](capacity),
	}

	runtime.SetFinalizer(c, func(c *Cache[K, V]) {
		c.backend.Close()
	})

	return c
}

// Available cache eviction policies.
const (
	Default = "default"
	LRU     = "lru"
	LFU     = "lfu"
)

// WithPolicy configures the cache with specified eviction policy.
func (c *Cache[K, V]) WithPolicy(policy string) *Cache[K, V] {
	if policy != "" {
		switch policy {
		case Default:
		case LRU:
			c.backend.Policy = backend.LRU
		case LFU:
			c.backend.Policy = backend.LFU
		default:
			panic("evcache: invalid eviction policy '" + policy + "'")
		}
	}
	return c
}

// Exists returns whether a value in the cache exists for key.
func (c *Cache[K, V]) Exists(key K) bool {
	_, ok := c.backend.Load(key)
	return ok
}

// Get returns the value stored in the cache for key.
func (c *Cache[K, V]) Get(key K) (value V, exists bool) {
	if elem, ok := c.backend.Load(key); ok {
		return elem.Value.Value, true
	}

	var zero V
	return zero, false
}

// Range calls f for each key and value present in the cache in no particular order or consistency.
// If f returns false, Range stops the iteration. It skips values that are currently being Fetched.
//
// Range is allowed to modify the cache.
func (c *Cache[K, V]) Range(f func(key K, value V) bool) {
	c.backend.Range(func(elem *ringlist.Element[backend.Record[K, V]]) bool {
		return f(elem.Value.Key, elem.Value.Value)
	})
}

// Len returns the number of keys in the cache.
func (c *Cache[K, V]) Len() int {
	return c.backend.Len()
}

// Evict a key and return its value.
func (c *Cache[K, V]) Evict(key K) (value V, ok bool) {
	if value, ok := c.backend.Evict(key); ok {
		return value, true
	}

	var zero V
	return zero, false
}

// LoadOrStore loads or stores a value for key. If the key is being Fetched, LoadOrStore
// blocks until Fetch returns.
func (c *Cache[K, V]) LoadOrStore(key K, ttl time.Duration, value V) (old V, loaded bool) {
	loaded = true

	v, _ := c.Fetch(key, ttl, func() (V, error) {
		loaded = false
		return value, nil
	})

	return v, loaded
}

// MustFetch fetches a value or panics if f panics.
func (c *Cache[K, V]) MustFetch(key K, ttl time.Duration, f func() V) (value V) {
	v, _ := c.TryFetch(key, func() (V, time.Duration, error) {
		value := f()
		return value, ttl, nil
	})
	return v
}

// Fetch loads or stores a value for key. If a value exists, f will not be called,
// otherwise f will be called to fetch the new value. It panics if f panics.
// Concurrent Fetches for the same key will block each other and return a single result.
func (c *Cache[K, V]) Fetch(key K, ttl time.Duration, f func() (V, error)) (value V, err error) {
	return c.TryFetch(key, func() (V, time.Duration, error) {
		value, err := f()
		return value, ttl, err
	})
}

// TryFetch is like Fetch but allows the TTL to be returned alongside the value from callback.
func (c *Cache[K, V]) TryFetch(key K, f func() (V, time.Duration, error)) (value V, err error) {
	newElem := c.backend.Reserve(key)

	if elem, loaded := c.backend.LoadOrStore(newElem); loaded {
		c.backend.Release(newElem)
		return elem.Value.Value, nil
	}

	defer func() {
		if r := recover(); r != nil {
			c.backend.Discard(newElem)

			panic(r)
		}
	}()

	value, ttl, err := f()
	if err != nil {
		c.backend.Discard(newElem)

		var zero V
		return zero, err
	}

	c.backend.Initialize(newElem, value, ttl)

	return value, nil
}
