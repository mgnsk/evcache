package backend_test

import (
	"cmp"
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/mgnsk/evcache/v3/internal/backend"
	. "github.com/mgnsk/evcache/v3/internal/testing"
)

func TestMapShrinkUninitializedRecords(t *testing.T) {
	size := 1000

	t.Run("realloc", func(t *testing.T) {
		b := backend.NewBackend[int, int](0)

		t.Log("filling the cache")
		for i := 0; i < size; i++ {
			elem, _, _ := b.LoadOrStore(i, b.Reserve())
			b.Initialize(elem, i, 0)
		}

		t.Log("asserting number of initialized elements is correct")
		Equal(t, b.Len(), size)
		Equal(t, getMapRangeCount(b), size)

		var elem *backend.Element[int]

		t.Log("storing a new uninitialized element")
		elem = b.Reserve()
		_, initialized, loaded := b.LoadOrStore(size, elem)
		Equal(t, initialized, false)
		Equal(t, loaded, false)

		t.Log("asserting number of initialized has not changed")
		Equal(t, b.Len(), size)
		Equal(t, getMapRangeCount(b), size)

		t.Log("evicting half of records")
		for i := 0; i < size/2; i++ {
			_, ok := b.Evict(i)
			Equal(t, ok, true)
		}

		t.Log("asserting number of initialized elements is correct")
		Equal(t, b.Len(), size/2)
		Equal(t, getMapRangeCount(b), size/2)

		// Initialize the element.
		b.Initialize(elem, 0, 0)

		t.Log("asserting number of initialized elements has changed")
		Equal(t, b.Len(), size/2+1)
		Equal(t, getMapRangeCount(b), size/2+1)
	})
}

func TestDeleteUninitializedElement(t *testing.T) {
	b := backend.NewBackend[int, int](0)

	t.Log("storing a new uninitialized element")
	elem := b.Reserve()
	_, initialized, loaded := b.LoadOrStore(0, elem)
	Equal(t, initialized, false)
	Equal(t, loaded, false)
	Equal(t, b.Len(), 0)

	_, evicted := b.Evict(0)
	Equal(t, evicted, false)
	Equal(t, b.Len(), 0)

	b.Delete(0)
	Equal(t, b.Len(), 0)
}

func TestOverflowEvictionOrder(t *testing.T) {
	const capacity = 10

	t.Run("insert order", func(t *testing.T) {
		t.Parallel()

		b := backend.NewBackend[int, *string](capacity)

		evictedKeys := fillCache(t, b, capacity)

		// Overflow the cache and catch evicted keys.
		keys := overflowAndCollectKeys(t, b, capacity, evictedKeys)

		Equal(t, len(keys), capacity)
		Equal(t, keys, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	})

	t.Run("LFU order", func(t *testing.T) {
		t.Parallel()

		b := backend.NewBackend[int, *string](capacity)
		b.Policy = backend.LFU

		evictedKeys := fillCache(t, b, capacity)
		_ = evictedKeys

		t.Log("creating LFU test usage")
		createLFUTestUsage(t, b, capacity)

		// Overflow the cache and catch evicted keys.
		keys := overflowAndCollectKeys(t, b, capacity, evictedKeys)

		Equal(t, len(keys), capacity)
		Equal(t, keys, []int{9, 8, 7, 6, 5, 4, 3, 2, 1, 0})
	})

	t.Run("LRU order", func(t *testing.T) {
		t.Parallel()

		b := backend.NewBackend[int, *string](capacity)
		b.Policy = backend.LRU

		evictedKeys := fillCache(t, b, capacity)
		_ = evictedKeys

		t.Log("creating LRU test usage")
		createLRUTestUsage(t, b, capacity)

		// Overflow the cache and catch evicted keys.
		keys := overflowAndCollectKeys(t, b, capacity, evictedKeys)

		Equal(t, len(keys), capacity)
		Equal(t, keys, []int{9, 8, 7, 6, 5, 4, 3, 2, 1, 0})
	})
}

func getMapRangeCount[K comparable, V any](b *backend.Backend[K, V]) int {
	n := 0

	b.Range(func(K, *backend.Element[V]) bool {
		n++
		return true
	})

	return n
}

func fillCache(t *testing.T, b *backend.Backend[int, *string], capacity int) <-chan int {
	evictedKeys := make(chan int, capacity)

	for i := 0; i < capacity; i++ {
		value := new(string)
		i := i
		runtime.SetFinalizer(value, func(any) {
			t.Logf("sending evicted key %d", i)
			evictedKeys <- i
		})

		elem := b.Reserve()

		_, _, loaded := b.LoadOrStore(i, elem)
		Equal(t, loaded, false)
		b.Initialize(elem, value, 0)
		Equal(t, b.Len(), i+1)
	}

	return evictedKeys
}

func createLFUTestUsage(t *testing.T, b *backend.Backend[int, *string], capacity int) {
	for i := 0; i < capacity; i++ {
		// Increase hit counts in list reverse order.
		hits := capacity * (capacity - i)
		t.Logf("LFU test usage: key=%d, hits=%d\n", i, hits)

		for n := 0; n < hits; n++ {
			_, loaded := b.Load(i)
			Equal(t, loaded, true)
		}
	}
}

func createLRUTestUsage(t *testing.T, b *backend.Backend[int, *string], capacity int) {
	// Hit the cache in reverse order.
	for i := capacity - 1; i >= 0; i-- {
		_, loaded := b.Load(i)
		Equal(t, loaded, true)
	}
}

func overflowAndCollectKeys(t *testing.T, b *backend.Backend[int, *string], capacity int, evictedKeys <-chan int) (keys []int) {
	t.Helper()

	for i := 0; i < capacity; i++ {
		i := i + capacity

		elem := b.Reserve()
		_, _, loaded := b.LoadOrStore(i, elem)
		Equal(t, loaded, false)
		b.Initialize(elem, nil, 0)

		EventuallyTrue(t, func() bool {
			runtime.GC()
			select {
			case key := <-evictedKeys:
				t.Logf("received evicted key %d", key)
				keys = append(keys, key)
				return true
			default:
				return false
			}
		})
	}

	return keys
}

func BenchmarkSliceLoop(b *testing.B) {
	b.Run("value elements", func(b *testing.B) {
		for _, n := range []int{
			1e3,
			1e4,
			1e5,
			1e6,
		} {
			runtime.GC()

			items := createSlice[int](n, nil)

			b.Run(fmt.Sprint(n), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					for _, elem := range items {
						if elem.Value > 0 {
							panic("expected zero")
						}
					}
				}

				b.StopTimer()

				nsPerElement := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / float64(len(items))
				b.ReportMetric(nsPerElement, "ns/element")
			})
		}
	})

	b.Run("atomic elements", func(b *testing.B) {
		for _, n := range []int{
			1e3,
			1e4,
			1e5,
			1e6,
		} {
			runtime.GC()

			items := createSlice[atomic.Uint64](n, nil)

			b.Run(fmt.Sprint(n), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					for _, elem := range items {
						if elem.Value.Load() > 0 {
							panic("expected zero")
						}
					}
				}

				b.StopTimer()

				nsPerElement := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / float64(len(items))
				b.ReportMetric(nsPerElement, "ns/element")
			})
		}
	})
}

func BenchmarkSliceSort(b *testing.B) {
	b.Run("value elements", func(b *testing.B) {
		for _, n := range []int{
			1e3,
			1e4,
			1e5,
			1e6,
		} {
			runtime.GC()

			items := createSlice(n, func(v *int) {
				*v = rand.Int()
			})

			b.Run(fmt.Sprint(n), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					slices.SortFunc(items, func(a, b *backend.Element[int]) int {
						return cmp.Compare(a.Value, b.Value)
					})
				}

				b.StopTimer()

				nsPerElement := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / float64(len(items))
				b.ReportMetric(nsPerElement, "ns/element")
			})
		}
	})

	b.Run("atomic elements", func(b *testing.B) {
		for _, n := range []int{
			1e3,
			1e4,
			1e5,
			1e6,
		} {
			runtime.GC()

			items := createSlice(n, func(u *atomic.Uint64) {
				u.Store(rand.Uint64())
			})

			b.Run(fmt.Sprint(n), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					slices.SortFunc(items, func(a, b *backend.Element[atomic.Uint64]) int {
						return cmp.Compare(a.Value.Load(), b.Value.Load())
					})
				}

				b.StopTimer()

				nsPerElement := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / float64(len(items))
				b.ReportMetric(nsPerElement, "ns/element")
			})
		}
	})
}

func createSlice[V any](n int, valueFn func(*V)) []*backend.Element[V] {
	items := make([]*backend.Element[V], n)
	for i := 0; i < len(items); i++ {
		items[i] = &backend.Element[V]{}
		if valueFn != nil {
			valueFn(&items[i].Value)
		}
	}

	return items
}
