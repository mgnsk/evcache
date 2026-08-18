package evcache_test

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/mgnsk/evcache/v4"
	. "github.com/mgnsk/evcache/v4/internal/testing"
)

func TestCacheStoreLoadHas(t *testing.T) {
	c := evcache.New[string, string]()

	_, loaded := c.Load("key")
	Equal(t, loaded, false)
	Equal(t, c.Has("key"), false)

	c.Store("key", "value")

	value, loaded := c.Load("key")
	Equal(t, loaded, true)
	Equal(t, value, "value")
	Equal(t, c.Has("key"), true)
}

func TestCacheLen(t *testing.T) {
	c := evcache.New[int, int]()

	Equal(t, c.Len(), 0)

	c.Store(1, 1)
	c.Store(2, 2)
	Equal(t, c.Len(), 2)
}

func TestCacheEvict(t *testing.T) {
	c := evcache.New[string, string]()

	_, evicted := c.Evict("key")
	Equal(t, evicted, false)

	c.Store("key", "value")

	value, evicted := c.Evict("key")
	Equal(t, evicted, true)
	Equal(t, value, "value")
	Equal(t, c.Has("key"), false)
}

func TestCacheRange(t *testing.T) {
	c := evcache.New[int, int]()

	c.Store(1, 10)
	c.Store(2, 20)
	c.Store(3, 30)

	seen := map[int]int{}
	c.Range(func(key, value int) bool {
		seen[key] = value
		return true
	})
	Equal(t, seen, map[int]int{1: 10, 2: 20, 3: 30})
}

func TestCacheRangeStopsEarly(t *testing.T) {
	c := evcache.New[int, int]()

	c.Store(1, 10)
	c.Store(2, 20)

	n := 0
	c.Range(func(key, value int) bool {
		n++
		return false
	})
	Equal(t, n, 1)
}

func TestCacheRangeMayModifyCache(t *testing.T) {
	c := evcache.New[int, int]()

	c.Store(1, 10)
	c.Store(2, 20)
	c.Store(3, 30)

	c.Range(func(key, value int) bool {
		c.Evict(key)
		c.Store(key+100, value)
		return true
	})

	Equal(t, c.Len(), 3)
	for _, key := range []int{1, 2, 3} {
		Equal(t, c.Has(key), false)
	}
	for _, key := range []int{101, 102, 103} {
		Equal(t, c.Has(key), true)
	}
}

func TestCacheFetchCachesValue(t *testing.T) {
	c := evcache.New[string, int]()

	calls := 0
	fetch := func() (int, error) {
		calls++
		return 1, nil
	}

	v, err := c.Fetch("key", fetch)
	Must(t, err)
	Equal(t, v, 1)

	v, err = c.Fetch("key", fetch)
	Must(t, err)
	Equal(t, v, 1)
	Equal(t, calls, 1)
}

func TestCacheFetchPropagatesError(t *testing.T) {
	c := evcache.New[string, int]()

	errFetch := errors.New("fetch failed")

	_, err := c.Fetch("key", func() (int, error) {
		return 0, errFetch
	})
	Equal(t, errors.Is(err, errFetch), true)
	Equal(t, c.Has("key"), false)
}

func TestCacheFetchTTL(t *testing.T) {
	c := evcache.New[string, int]()

	_, err := c.FetchTTL("key", func() (int, time.Duration, error) {
		return 1, time.Millisecond, nil
	})
	Must(t, err)
	Equal(t, c.Has("key"), true)

	// New() applies the default 1s expiry debounce (there is no public
	// option to disable it), so a short TTL can take up to just under
	// 2x the debounce interval to actually expire. Give this plenty of
	// margin above that worst case rather than relying on the 1s
	// default EventuallyTrue timeout.
	EventuallyTrue(t, func() bool {
		return c.Len() == 0
	}, 3*time.Second)
}

func TestCacheGoGC(t *testing.T) {
	capacity := 1_000_000
	c := evcache.New[int, struct{}](evcache.WithCapacity(capacity))

	for i := range capacity {
		c.StoreTTL(i, struct{}{}, time.Hour) // Store with TTL to trigger the cleanup runner.
	}

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	t.Logf("alloc before:\t%d bytes", stats.Alloc)
	oldSize := stats.Alloc

	runtime.KeepAlive(c)

	EventuallyTrue(t, func() bool {
		runtime.GC()
		runtime.ReadMemStats(&stats)

		newSize := stats.Alloc

		return newSize < oldSize/2
	})

	t.Logf("alloc after:\t%d bytes", stats.Alloc)
}
