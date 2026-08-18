package evcache_test

import (
	"testing"
	"time"

	"github.com/mgnsk/evcache/v4"
	. "github.com/mgnsk/evcache/v4/internal/testing"
)

func TestWithPolicyInvalidPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected New to panic on invalid policy")
		}
	}()

	evcache.New[int, int](evcache.WithPolicy("bogus"))
}

func TestWithPolicyFIFO(t *testing.T) {
	for _, policy := range []string{"", evcache.FIFO} {
		c := evcache.New[int, int](evcache.WithCapacity(2), evcache.WithPolicy(policy))

		c.Store(1, 1)
		c.Store(2, 2)
		c.Store(3, 3) // Overflows the cache, evicting the oldest key.

		Equal(t, c.Has(1), false)
		Equal(t, c.Has(2), true)
		Equal(t, c.Has(3), true)
	}
}

func TestWithPolicyLRU(t *testing.T) {
	c := evcache.New[int, int](evcache.WithCapacity(2), evcache.WithPolicy(evcache.LRU))

	c.Store(1, 1)
	c.Store(2, 2)
	c.Load(1) // Touch key 1, making key 2 the least recently used.
	c.Store(3, 3)

	Equal(t, c.Has(1), true)
	Equal(t, c.Has(2), false)
	Equal(t, c.Has(3), true)
}

func TestWithPolicyLFU(t *testing.T) {
	c := evcache.New[int, int](evcache.WithCapacity(2), evcache.WithPolicy(evcache.LFU))

	c.Store(1, 1)
	c.Store(2, 2)
	c.Load(1) // Increase key 1's hit count, making key 2 the least frequently used.
	c.Store(3, 3)

	Equal(t, c.Has(1), true)
	Equal(t, c.Has(2), false)
	Equal(t, c.Has(3), true)
}

func TestWithTTL(t *testing.T) {
	c := evcache.New[int, int](evcache.WithTTL(time.Millisecond))

	c.Store(1, 1)
	Equal(t, c.Has(1), true)

	// New() applies the default 1s expiry debounce (there is no public
	// option to disable it), so a short TTL can take up to just under
	// 2x the debounce interval to actually expire.
	EventuallyTrue(t, func() bool {
		return c.Len() == 0
	}, 3*time.Second)
}
