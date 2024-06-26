package evcache_test

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/mgnsk/evcache/v3"
	. "github.com/mgnsk/evcache/v3/internal/testing"
)

func TestLoadOrStoreNotExists(t *testing.T) {
	c := evcache.New[string, int](0)

	_, loaded := c.LoadOrStore("key", 0, 1)
	Equal(t, loaded, false)
	Equal(t, c.Exists("key"), true)
	Equal(t, c.Len(), 1)
}

func TestLoadOrStoreExists(t *testing.T) {
	c := evcache.New[string, int](0)

	c.LoadOrStore("key", 0, 1)

	v, loaded := c.LoadOrStore("key", 0, 2)
	Equal(t, loaded, true)
	Equal(t, v, 1)
}

func TestFetchCallbackBlocks(t *testing.T) {
	c := evcache.New[string, string](0)

	done := make(chan struct{})
	fetchStarted := make(chan struct{})
	defer close(done)

	go func() {
		_, _ = c.Fetch("key", 0, func() (string, error) {
			close(fetchStarted)
			<-done
			return "", nil
		})
	}()

	<-fetchStarted

	t.Run("non-blocking Exists", func(t *testing.T) {
		Equal(t, c.Exists("key"), false)
	})

	t.Run("non-blocking Evict", func(t *testing.T) {
		_, ok := c.Evict("key")
		Equal(t, ok, false)
	})

	t.Run("non-blocking Get", func(t *testing.T) {
		_, ok := c.Get("key")
		Equal(t, ok, false)
	})

	t.Run("autoexpiry for other keys works", func(t *testing.T) {
		c.LoadOrStore("key1", time.Millisecond, "value1")

		EventuallyTrue(t, func() bool {
			return !c.Exists("key1")
		})
	})

	t.Run("non-blocking Range", func(t *testing.T) {
		c.LoadOrStore("key1", 0, "value1")

		n := 0
		c.Range(func(key, value string) bool {
			if key == "key" {
				t.Fatal("expected to skip key")
			}

			v, ok := c.Evict(key)
			Equal(t, ok, true)
			Equal(t, v, value)
			Equal(t, c.Len(), 0)

			n++
			return true
		})

		Equal(t, n, 1)
	})
}

func TestFetchCallbackPanic(t *testing.T) {
	c := evcache.New[string, string](0)

	func() {
		defer func() {
			_ = recover()
		}()

		_, _ = c.Fetch("key", 0, func() (string, error) {
			panic("failed")
		})
	}()

	// Fetching again does not deadlock as the uninitialized value was cleaned up.
	v, err := c.Fetch("key", 0, func() (string, error) {
		return "new value", nil
	})

	Must(t, err)
	Equal(t, v, "new value")
}

func TestConcurrentFetch(t *testing.T) {
	t.Run("returns error", func(t *testing.T) {
		c := evcache.New[string, string](0)

		errCh := make(chan error)
		fetchStarted := make(chan struct{})

		go func() {
			_, _ = c.Fetch("key", 0, func() (string, error) {
				close(fetchStarted)
				return "", <-errCh
			})
		}()

		<-fetchStarted
		errCh <- fmt.Errorf("error fetching value")

		v, err := c.Fetch("key", 0, func() (string, error) {
			return "value", nil
		})

		Must(t, err)
		Equal(t, v, "value")
	})

	t.Run("returns value", func(t *testing.T) {
		c := evcache.New[string, string](0)

		valueCh := make(chan string)
		fetchStarted := make(chan struct{})

		go func() {
			_, _ = c.Fetch("key", 0, func() (string, error) {
				close(fetchStarted)
				return <-valueCh, nil
			})
		}()

		<-fetchStarted
		valueCh <- "value"

		v, err := c.Fetch("key", 0, func() (string, error) {
			return "value1", nil
		})

		Must(t, err)
		Equal(t, v, "value")
	})
}

func TestEvict(t *testing.T) {
	c := evcache.New[string, string](0)

	c.LoadOrStore("key", 0, "value")

	v, ok := c.Evict("key")

	Equal(t, ok, true)
	Equal(t, v, "value")
	Equal(t, c.Exists("key"), false)
}

func TestOverflow(t *testing.T) {
	capacity := 100
	c := evcache.New[int, int](capacity)

	for i := 0; i < 2*capacity; i++ {
		c.LoadOrStore(i, 0, 0)
	}

	Equal(t, c.Len(), capacity)
}

func TestExpire(t *testing.T) {
	c := evcache.New[int, int](0)

	n := 10
	for i := 0; i < n; i++ {
		// Store records in descending TTL order.
		d := time.Duration(n-i) * time.Millisecond
		_, _ = c.LoadOrStore(i, d, 0)
	}

	EventuallyTrue(t, func() bool {
		return c.Len() == 0
	})
}

func TestExpireEdgeCase(t *testing.T) {
	c := evcache.New[int, *string](0)

	v1 := new(string)

	_, loaded := c.LoadOrStore(0, 10*time.Millisecond, v1)
	Equal(t, loaded, false)
	Equal(t, c.Len(), 1)

	// Wait until v1 expires.
	EventuallyTrue(t, func() bool {
		return c.Len() == 0
	})

	// Assert that after v1 expires, v2 with a longer TTL than v1, can expire,
	// specifically that backend's GC loop resets earliestExpireAt to zero,
	// so that LoadOrStore schedules the GC loop.
	v2 := new(string)
	_, loaded = c.LoadOrStore(1, 100*time.Millisecond, v2)
	Equal(t, loaded, false)
	Equal(t, c.Len(), 1)

	EventuallyTrue(t, func() bool {
		return c.Len() == 0
	})
}

func TestCacheGoGC(t *testing.T) {
	capacity := 1_000_000
	c := evcache.New[int, byte](capacity)

	for i := range capacity {
		c.LoadOrStore(i, 0, 0)
	}

	var stats runtime.MemStats

	runtime.ReadMemStats(&stats)
	t.Logf("alloc before:\t%d bytes", stats.Alloc)

	runtime.KeepAlive(c)

	// Run GC twice to account for the finalizer.
	runtime.GC()
	runtime.GC()

	runtime.ReadMemStats(&stats)
	t.Logf("alloc after:\t%d bytes", stats.Alloc)
}
