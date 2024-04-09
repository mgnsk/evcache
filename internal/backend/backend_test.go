package backend_test

import (
	"maps"
	"runtime"
	"testing"
	"time"

	"github.com/mgnsk/evcache/v3/internal/backend"
	. "github.com/mgnsk/evcache/v3/internal/testing"
	"github.com/mgnsk/ringlist"
)

func TestReallocUninitializedRecords(t *testing.T) {
	size := 1000

	t.Run("realloc", func(t *testing.T) {
		b := backend.NewBackend[int, int](0)

		t.Log("filling the cache")
		for i := 0; i < size; i++ {
			elem, _ := b.LoadOrStore(i, b.Reserve())
			b.Initialize(elem, i, 0)
		}

		t.Log("asserting number of initialized elements is correct")
		assertCacheLen(t, b, size)

		var elem *ringlist.Element[backend.Record[int]]

		t.Log("storing a new uninitialized element")
		elem = b.Reserve()
		_, loaded := b.LoadOrStore(size, elem)
		Equal(t, loaded, false)

		t.Log("asserting number of initialized has not changed")
		assertCacheLen(t, b, size)

		t.Log("evicting half of records")
		for i := 0; i < size/2; i++ {
			_, ok := b.Evict(i)
			Equal(t, ok, true)
		}

		t.Log("by running GC to force realloc")
		b.RunGC(time.Now().UnixNano())

		t.Log("asserting number of initialized elements is correct")
		assertCacheLen(t, b, size/2)

		// Initialize the element.
		b.Initialize(elem, 0, 0)

		t.Log("asserting number of initialized elements has changed")
		assertCacheLen(t, b, size/2+1)
	})
}

func TestDeleteUninitializedElement(t *testing.T) {
	b := backend.NewBackend[int, int](0)

	t.Log("storing a new uninitialized element")
	elem := b.Reserve()
	_, loaded := b.LoadOrStore(0, elem)
	Equal(t, loaded, false)
	assertCacheLen(t, b, 0)

	_, evicted := b.Evict(0)
	Equal(t, evicted, false)
	assertCacheLen(t, b, 0)

	b.Discard(0, elem)
	assertCacheLen(t, b, 0)
}

func TestOverflowEvictionOrder(t *testing.T) {
	const capacity = 10

	t.Run("insert order", func(t *testing.T) {
		t.Parallel()

		b := backend.NewBackend[int, int](capacity)

		fillCache(t, b, capacity)

		// Overflow the cache and catch evicted keys.
		keys := overflowAndCollectKeys(t, b, capacity)

		Equal(t, len(keys), capacity)
		Equal(t, keys, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	})

	t.Run("LFU order", func(t *testing.T) {
		t.Parallel()

		b := backend.NewBackend[int, int](capacity)
		b.Policy = backend.LFU

		fillCache(t, b, capacity)

		t.Log("creating LFU test usage")
		createLFUTestUsage(t, b, capacity)

		// Overflow the cache and catch evicted keys.
		keys := overflowAndCollectKeys(t, b, capacity)

		Equal(t, len(keys), capacity)
		Equal(t, keys, []int{9, 8, 7, 6, 5, 4, 3, 2, 1, 0})
	})

	t.Run("LRU order", func(t *testing.T) {
		t.Parallel()

		b := backend.NewBackend[int, int](capacity)
		b.Policy = backend.LRU

		fillCache(t, b, capacity)

		t.Log("creating LRU test usage")
		createLRUTestUsage(t, b, capacity)

		// Overflow the cache and catch evicted keys.
		keys := overflowAndCollectKeys(t, b, capacity)

		Equal(t, len(keys), capacity)
		Equal(t, keys, []int{9, 8, 7, 6, 5, 4, 3, 2, 1, 0})
	})
}

func TestMapShrink(t *testing.T) {
	n := 100000

	t.Run("maps.Copy()", func(t *testing.T) {
		m := map[int]int{}

		for i := 0; i < n; i++ {
			m[i] = i
		}

		t.Logf("after setting values: %dKB", getMemStats()/1024)
		Equal(t, len(m), n)

		for i := 0; i < n-1; i++ {
			delete(m, i)
		}

		t.Logf("after deleting all but one: %dKB", getMemStats()/1024)
		Equal(t, len(m), 1)

		newM := map[int]int{}
		maps.Copy(newM, m)

		t.Logf("after copying map: %dKB", getMemStats()/1024)
		Equal(t, len(newM), 1)
	})

	t.Run("maps.Clone()", func(t *testing.T) {
		m := map[int]int{}

		for i := 0; i < n; i++ {
			m[i] = i
		}

		t.Logf("after setting values: %dKB", getMemStats()/1024)
		Equal(t, len(m), n)

		for i := 0; i < n-1; i++ {
			delete(m, i)
		}

		t.Logf("after deleting all but one: %dKB", getMemStats()/1024)
		Equal(t, len(m), 1)

		m = maps.Clone(m)

		t.Logf("after cloning map: %dKB", getMemStats()/1024)
		Equal(t, len(m), 1)
	})
}

func getMemStats() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc
}

func getMapRangeCount[K comparable, V any](b *backend.Backend[K, V]) int {
	n := 0

	b.Range(func(K, *ringlist.Element[backend.Record[V]]) bool {
		n++
		return true
	})

	return n
}

func assertCacheLen[K comparable, V any](t *testing.T, be *backend.Backend[K, V], n int) {
	t.Helper()

	Equal(t, be.Len(), n)
	Equal(t, getMapRangeCount(be), n)
}

func fillCache(t *testing.T, b *backend.Backend[int, int], capacity int) {
	for i := 0; i < capacity; i++ {
		elem := b.Reserve()
		_, loaded := b.LoadOrStore(i, elem)
		Equal(t, loaded, false)
		b.Initialize(elem, 0, 0)
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
		b.Range(func(key int, _ *ringlist.Element[backend.Record[int]]) bool {
			oldKeys[key] = struct{}{}
			return true
		})
		Equal(t, len(oldKeys), capacity)

		t.Logf("store: %v", i)
		elem := b.Reserve()
		_, loaded := b.LoadOrStore(i, elem)
		Equal(t, loaded, false)
		b.Initialize(elem, 0, 0)

		t.Log("expecting GC to run")
		EventuallyTrue(t, func() bool {
			return b.Len() == capacity
		})

		t.Log("collecting new cache state")
		newKeys := map[int]struct{}{}
		b.Range(func(key int, _ *ringlist.Element[backend.Record[int]]) bool {
			newKeys[key] = struct{}{}
			return true
		})

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
