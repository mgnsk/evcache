package evcache

import (
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
//
// The zero values configures unbounded capacity.
func WithCapacity(capacity int) Option {
	return funcOption(func(opts *cacheOptions) {
		opts.capacity = capacity
	})
}

// WithPolicy option configures the cache with specified eviction policy.
//
// The zero value configures the FIFO policy.
func WithPolicy(policy string) Option {
	return funcOption(func(opts *cacheOptions) {
		switch policy {
		case "":
			opts.policy = FIFO

		case FIFO, LRU, LFU:
			opts.policy = policy

		default:
			panic("evcache: invalid eviction policy '" + policy + "'")
		}
	})
}

// WithTTL option configures the cache with specified default TTL.
//
// The zero value configures infinite default TTL.
func WithTTL(ttl time.Duration) Option {
	return funcOption(func(opts *cacheOptions) {
		opts.ttl = ttl
	})
}

// WithExpiryDebounce returns an option that configures the cache with specified expiry eviction debounce duration.
//
// The zero value configures the default 1s debounce duration.
func WithExpiryDebounce(debounce time.Duration) Option {
	return funcOption(func(opts *cacheOptions) {
		if debounce == 0 {
			opts.debounce = 1 * time.Second
		} else {
			opts.debounce = debounce
		}
	})
}

type funcOption func(*cacheOptions)

func (o funcOption) apply(opts *cacheOptions) {
	o(opts)
}
