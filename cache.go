/*
Package evcache implements a concurrent key-value cache with capacity overflow eviction, item expiry and deduplication.
*/
package evcache

import (
	"runtime"
	"time"

	"github.com/mgnsk/evcache/v3/internal/backend"
)

// Available cache eviction policies.
const (
	// FIFO policy orders recods in FIFO order.
	FIFO = backend.FIFO
	// LFU policy orders records in LFU order.
	LFU = backend.LFU
	// LRU policy orders records in LRU order.
	LRU = backend.LRU
)

// Option is a cache configuration option.
type Option interface {
	apply(*cacheOptions)
}

type cacheOptions struct {
	policy   string
	capacity int
	ttl      time.Duration
	debounce time.Duration
}

// WithCapacity option configures the cache with specified capacity.
func WithCapacity(capacity int) Option {
	return funcOption(func(opts *cacheOptions) {
		opts.capacity = capacity
	})
}

// WithPolicy option configures the cache with specified eviction policy.
func WithPolicy(policy string) Option {
	return funcOption(func(opts *cacheOptions) {
		switch policy {
		case FIFO, LRU, LFU:
			opts.policy = policy

		default:
			panic("evcache: invalid eviction policy '" + policy + "'")
		}
	})
}

// WithTTL option configures the cache with specified default TTL.
func WithTTL(ttl time.Duration) Option {
	return funcOption(func(opts *cacheOptions) {
		opts.ttl = ttl
	})
}

// WithExpiryDebounce returns an option that configures the cache with specified expiry eviction debounce duration.
func WithExpiryDebounce(debounce time.Duration) Option {
	return funcOption(func(opts *cacheOptions) {
		opts.debounce = debounce
	})
}

type funcOption func(*cacheOptions)

func (o funcOption) apply(opts *cacheOptions) {
	o(opts)
}

// Cache is an in-memory cache.
type Cache[K comparable, V any] struct {
	backend *backend.Backend[K, V]
	ttl     time.Duration
}

// New creates a new empty cache.
func New[K comparable, V any](opt ...Option) *Cache[K, V] {
	opts := cacheOptions{
		debounce: time.Second,
	}

	for _, o := range opt {
		o.apply(&opts)
	}

	be := &backend.Backend[K, V]{}
	be.Init(opts.capacity, opts.policy, opts.ttl, opts.debounce)

	c := &Cache[K, V]{
		backend: be,
		ttl:     opts.ttl,
	}

	runtime.SetFinalizer(c, func(c *Cache[K, V]) {
		c.backend.Close()
	})

	return c
}

// Keys returns initialized cache keys in the sort order specified by policy.
func (c *Cache[K, V]) Keys() []K {
	return c.backend.Keys()
}

// Len returns the number of keys in the cache.
func (c *Cache[K, V]) Len() int {
	return c.backend.Len()
}

// Load an element from the cache.
func (c *Cache[K, V]) Load(key K) (value V, loaded bool) {
	return c.backend.Load(key)
}

// Evict a key and return its value.
func (c *Cache[K, V]) Evict(key K) (value V, ok bool) {
	return c.backend.Evict(key)
}

// Store an element.
func (c *Cache[K, V]) Store(key K, value V) {
	c.backend.Store(key, value)
}

// StoreTTL stores an element with specified TTL.
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
