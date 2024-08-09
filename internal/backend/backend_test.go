package backend_test

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mgnsk/evcache/v3/internal/backend"
	. "github.com/mgnsk/evcache/v3/internal/testing"
)

func TestFetchCallbackBlocks(t *testing.T) {
	var b backend.Backend[string, string]
	b.Init(0, "", 0, 0)
	t.Cleanup(b.Close)

	wg := sync.WaitGroup{}
	done := make(chan struct{})
	onceDone := sync.OnceFunc(func() {
		close(done)
	})
	fetchStarted := make(chan struct{})

	t.Cleanup(func() {
		onceDone()
		wg.Wait()
	})

	wg.Add(1)
	go func() {
		defer wg.Done()

		_, _ = b.Fetch("key", func() (string, error) {
			t.Log("Fetch started")
			close(fetchStarted)

			<-done

			return "", nil
		})
	}()

	<-fetchStarted

	t.Run("assert cache empty", func(t *testing.T) {
		Equal(t, b.Len(), 0)
	})

	t.Run("non-blocking Evict", func(t *testing.T) {
		_, ok := b.Evict("key")
		Equal(t, ok, false)
	})

	t.Run("autoexpiry for other keys works", func(t *testing.T) {
		b.FetchTTL("key1", func() (string, time.Duration, error) {
			return "value1", time.Millisecond, nil
		})

		EventuallyTrue(t, func() bool {
			return b.Len() == 0
		})
	})

	t.Run("non-blocking Has", func(t *testing.T) {
		b.Fetch("key1", func() (string, error) {
			return "value1", nil
		})

		Equal(t, b.Has("key1"), true)
	})

	t.Run("non-blocking Keys", func(t *testing.T) {
		b.Fetch("key1", func() (string, error) {
			return "value1", nil
		})

		keys := b.Keys()
		Equal(t, len(keys), 1)
		Equal(t, keys[0], "key1")
	})

	t.Run("non-blocking Range", func(t *testing.T) {
		b.Fetch("key1", func() (string, error) {
			return "value1", nil
		})

		var keys []string
		b.Range(func(key string, _ string) bool {
			keys = append(keys, key)
			return true
		})
		Equal(t, len(keys), 1)
		Equal(t, keys[0], "key1")
	})

	t.Run("Store discards the key", func(t *testing.T) {
		b.Store("key", "value1")

		value, loaded := b.Load("key")
		Equal(t, loaded, true)
		Equal(t, value, "value1")

		t.Log("assert that overwritten value exists after Fetch returns")

		// TODO: test setup scope
		onceDone()
		wg.Wait()

		value, loaded = b.Load("key")
		Equal(t, loaded, true)
		Equal(t, value, "value1")
	})
}

func TestFetchCallbackPanic(t *testing.T) {
	var b backend.Backend[string, string]
	b.Init(0, "", 0, 0)
	t.Cleanup(b.Close)

	func() {
		defer func() {
			_ = recover()
		}()

		_, _ = b.Fetch("key", func() (string, error) {
			panic("failed")
		})
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)

		v, err := b.Fetch("key", func() (string, error) {
			return "new value", nil
		})

		Must(t, err)
		Equal(t, v, "new value")
	}()

	t.Log("assert that fetching again does not deadlock due to uninitialized value was cleaned up")

	EventuallyTrue(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
}

func TestConcurrentFetch(t *testing.T) {
	t.Run("returns error", func(t *testing.T) {
		var b backend.Backend[string, string]
		b.Init(0, "", 0, 0)
		t.Cleanup(b.Close)

		errCh := make(chan error)
		fetchStarted := make(chan struct{})

		go func() {
			_, _ = b.Fetch("key", func() (string, error) {
				close(fetchStarted)
				return "", <-errCh
			})
		}()

		<-fetchStarted
		errCh <- fmt.Errorf("error fetching value")

		v, err := b.Fetch("key", func() (string, error) {
			return "value", nil
		})

		Must(t, err)
		Equal(t, v, "value")
	})

	t.Run("returns value", func(t *testing.T) {
		var b backend.Backend[string, string]
		b.Init(0, "", 0, 0)
		t.Cleanup(b.Close)

		valueCh := make(chan string)
		fetchStarted := make(chan struct{})

		go func() {
			_, _ = b.Fetch("key", func() (string, error) {
				close(fetchStarted)
				return <-valueCh, nil
			})
		}()

		<-fetchStarted
		valueCh <- "value"

		v, err := b.Fetch("key", func() (string, error) {
			return "value1", nil
		})

		Must(t, err)
		Equal(t, v, "value")
	})
}

func TestExpiryLoopDebounce(t *testing.T) {
	debounce := 100 * time.Millisecond
	n := 10

	getHalfTimeLength := func(b *backend.Backend[int, int]) int {
		itemTTL := debounce / time.Duration(n)

		// Store elements with 10, 20, 30, ... ms TTL.
		for i := 0; i < n; i++ {
			b.StoreTTL(i, 0, time.Duration(i+1)*itemTTL)
		}

		time.Sleep(debounce / 2)

		return b.Len()
	}

	var debounceDisabledLen int
	{
		// TODO: t.Cleanup(b.Close) everywhere
		var b backend.Backend[int, int]
		b.Init(0, "", 0, 0)
		t.Cleanup(b.Close)

		debounceDisabledLen = getHalfTimeLength(&b)
	}

	var debounceEnabledLen int
	{
		var b backend.Backend[int, int]
		b.Init(0, "", 0, 100*time.Millisecond)
		t.Cleanup(b.Close)

		debounceEnabledLen = getHalfTimeLength(&b)
	}

	t.Log("assert that debounce disabled expires elements earlier than debounce enabled")
	Equal(t, debounceDisabledLen < debounceEnabledLen, true)
}

func TestEvict(t *testing.T) {
	var b backend.Backend[string, string]
	b.Init(0, "", 0, 0)
	t.Cleanup(b.Close)

	b.Store("key", "value")
	Equal(t, b.Len(), 1)

	v, ok := b.Evict("key")
	Equal(t, ok, true)
	Equal(t, v, "value")
	Equal(t, b.Len(), 0)
}

func TestOverflow(t *testing.T) {
	capacity := 100

	var b backend.Backend[int, int]
	b.Init(capacity, "", 0, 0)
	t.Cleanup(b.Close)

	for i := 0; i < 2*capacity; i++ {
		b.Store(i, 0)
		Equal(t, b.Len() <= capacity, true)
	}
}

func TestOverflowEvictionOrder(t *testing.T) {
	const capacity = 10

	t.Run("insert order", func(t *testing.T) {
		t.Parallel()

		var b backend.Backend[int, int]
		b.Init(capacity, "", 0, 0)
		t.Cleanup(b.Close)

		fillCache(t, &b, capacity)

		// Overflow the cache and catch evicted keys.
		keys := overflowAndCollectKeys(t, &b, capacity)

		Equal(t, len(keys), capacity)
		Equal(t, keys, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	})

	t.Run("LFU order", func(t *testing.T) {
		t.Parallel()

		var b backend.Backend[int, int]
		b.Init(capacity, backend.LFU, 0, 0)
		t.Cleanup(b.Close)

		fillCache(t, &b, capacity)

		t.Log("creating LFU test usage")
		createLFUTestUsage(t, &b, capacity)

		// Overflow the cache and catch evicted keys.
		keys := overflowAndCollectKeys(t, &b, capacity)

		Equal(t, len(keys), capacity)
		Equal(t, keys, []int{9, 8, 7, 6, 5, 4, 3, 2, 1, 0})
	})

	t.Run("LRU order", func(t *testing.T) {
		t.Parallel()

		var b backend.Backend[int, int]
		b.Init(capacity, backend.LRU, 0, 0)
		t.Cleanup(b.Close)

		fillCache(t, &b, capacity)

		t.Log("creating LRU test usage")
		createLRUTestUsage(t, &b, capacity)

		// Overflow the cache and catch evicted keys.
		keys := overflowAndCollectKeys(t, &b, capacity)

		Equal(t, len(keys), capacity)
		Equal(t, keys, []int{9, 8, 7, 6, 5, 4, 3, 2, 1, 0})
	})
}

func TestExpire(t *testing.T) {
	var b backend.Backend[int, int]
	b.Init(0, "", 0, 0)
	t.Cleanup(b.Close)

	n := 10
	for i := 0; i < n; i++ {
		// Store records in descending TTL order.
		b.StoreTTL(i, 0, time.Duration(n-1)*time.Millisecond)
	}

	EventuallyTrue(t, func() bool {
		return b.Len() == 0
	})
}

func TestExpireEdgeCase(t *testing.T) {
	var b backend.Backend[int, *string]
	b.Init(0, "", 0, 0)
	t.Cleanup(b.Close)

	v1 := new(string)

	b.StoreTTL(0, v1, 10*time.Millisecond)
	Equal(t, b.Len(), 1)

	// Wait until v1 expires.
	EventuallyTrue(t, func() bool {
		return b.Len() == 0
	})

	// Assert that after v1 expires, v2 with a longer TTL than v1, can expire,
	// specifically that backend's GC loop resets earliestExpireAt to zero,
	// so that LoadOrStore schedules the GC loop.
	v2 := new(string)
	b.StoreTTL(1, v2, 100*time.Millisecond)
	Equal(t, b.Len(), 1)

	EventuallyTrue(t, func() bool {
		return b.Len() == 0
	})
}

func getMemStats() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc
}

func assertCacheLen[K comparable, V any](t *testing.T, be *backend.Backend[K, V], n int) {
	t.Helper()

	Equal(t, be.Len(), n)
}

func fillCache[V any](t *testing.T, b *backend.Backend[int, V], capacity int) {
	for i := 0; i < capacity; i++ {
		b.Store(i, *new(V))
	}

	assertCacheLen(t, b, capacity)
}

func createLFUTestUsage(t *testing.T, b *backend.Backend[int, int], capacity int) {
	t.Helper()

	for i := 0; i < capacity; i++ {
		// Increase hit counts in list reverse order.
		hits := capacity - i - 1

		t.Logf("LFU test usage: key=%d, hits=%d\n", i, hits)

		for n := 0; n < hits; n++ {
			_, loaded := b.Load(i)
			Equal(t, loaded, true)
		}
	}
}

func createLRUTestUsage(t *testing.T, b *backend.Backend[int, int], capacity int) {
	t.Helper()

	// Hit the cache in reverse order.
	for i := capacity - 1; i >= 0; i-- {
		_, loaded := b.Load(i)
		Equal(t, loaded, true)
	}
}

func overflowAndCollectKeys(t *testing.T, b *backend.Backend[int, int], capacity int) (result []int) {
	t.Helper()

	for i := 0; i < capacity; i++ {
		i := i + capacity

		assertCacheLen(t, b, capacity)

		// Collect all cache keys, then overflow the cache and observe which key disappears.
		t.Log("collecting current cache state")
		oldKeys := map[int]struct{}{}
		for _, key := range b.Keys() {
			oldKeys[key] = struct{}{}
		}
		Equal(t, len(oldKeys), capacity)

		t.Logf("store: %v", i)
		b.Store(i, 0)

		t.Logf("expected overflowed element was evicted")
		assertCacheLen(t, b, capacity)

		t.Log("collecting new cache state")
		newKeys := map[int]struct{}{}
		for _, key := range b.Keys() {
			newKeys[key] = struct{}{}
		}
		Equal(t, len(oldKeys), capacity)

		t.Log("determining the evicted key")
		var missingKeys []int
		for key := range oldKeys {
			if _, ok := newKeys[key]; !ok {
				missingKeys = append(missingKeys, key)
			}
		}
		Equal(t, len(missingKeys), 1)

		t.Logf("received evicted key %d", missingKeys[0])
		result = append(result, missingKeys[0])
	}

	return result
}
