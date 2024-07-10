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
)

func BenchmarkSliceLoop(b *testing.B) {
	b.Run("value elements", func(b *testing.B) {
		for _, n := range []int{
			1e3,
			1e4,
			1e5,
			1e6,
		} {
			b.Run(fmt.Sprint(n), newTimePerElementBench(
				func() ([]*backend.Record[int, int], int) {
					items := createSlice[int, int](n, nil)
					return items, len(items)
				},
				func(items []*backend.Record[int, int]) {
					for _, elem := range items {
						if elem.Value > 0 {
							panic("expected zero")
						}
					}
				},
			))
		}
	})

	b.Run("atomic elements", func(b *testing.B) {
		for _, n := range []int{
			1e3,
			1e4,
			1e5,
			1e6,
		} {
			b.Run(fmt.Sprint(n), newTimePerElementBench(
				func() ([]*backend.Record[int, atomic.Uint64], int) {
					items := createSlice[int, atomic.Uint64](n, nil)
					return items, len(items)
				},
				func(items []*backend.Record[int, atomic.Uint64]) {
					for _, elem := range items {
						if elem.Value.Load() > 0 {
							panic("expected zero")
						}
					}
				},
			))
		}
	})
}

func BenchmarkMapIter(b *testing.B) {
	for _, n := range []int{
		1e3,
		1e4,
		1e5,
		1e6,
	} {
		b.Run(fmt.Sprint(n), newTimePerElementBench(
			func() (map[int]*backend.Record[int, int], int) {
				m := createMap[int, int](n, nil)
				return m, n
			},
			func(m map[int]*backend.Record[int, int]) {
				for _, elem := range m {
					if elem.Value > 0 {
						panic("expected zero")
					}
				}
			},
		))
	}
}

func BenchmarkSliceSort(b *testing.B) {
	b.Run("value elements", func(b *testing.B) {
		for _, n := range []int{
			1e3,
			1e4,
			1e5,
			1e6,
		} {
			b.Run(fmt.Sprint(n), newTimePerElementBench(
				func() ([]*backend.Record[int, int], int) {
					items := createSlice[int, int](n, func(_ *int, v *int) {
						*v = rand.Int()
					})
					return items, len(items)
				},
				func(items []*backend.Record[int, int]) {
					slices.SortFunc(items, func(a, b *backend.Record[int, int]) int {
						return cmp.Compare(a.Value, b.Value)
					})
				},
			))
		}
	})

	b.Run("atomic elements", func(b *testing.B) {
		for _, n := range []int{
			1e3,
			1e4,
			1e5,
			1e6,
		} {
			b.Run(fmt.Sprint(n), newTimePerElementBench(
				func() ([]*backend.Record[int, atomic.Uint64], int) {
					items := createSlice(n, func(_ *int, u *atomic.Uint64) {
						u.Store(rand.Uint64())
					})
					return items, len(items)
				},
				func(items []*backend.Record[int, atomic.Uint64]) {
					slices.SortFunc(items, func(a, b *backend.Record[int, atomic.Uint64]) int {
						return cmp.Compare(a.Value.Load(), b.Value.Load())
					})
				},
			))
		}
	})
}

func BenchmarkBackendGC(b *testing.B) {
	for _, n := range []int{
		1e3,
		1e4,
		1e5,
		1e6,
	} {
		b.Run(fmt.Sprint(n), newTimePerElementBench(
			func() (*backend.Backend[int, int], int) {
				var be backend.Backend[int, int]
				be.Init(n, "", 0, 0)
				for i := 0; i < n; i++ {
					be.Store(i, 0)
				}

				return &be, be.Len()
			},
			func(be *backend.Backend[int, int]) {
				keys := be.Keys()
				runtime.KeepAlive(keys)
			},
		))
	}
}

func BenchmarkBackendGCLFU(b *testing.B) {
	for _, n := range []int{
		1e3,
		1e4,
		1e5,
		1e6,
	} {
		b.Run(fmt.Sprint(n), newTimePerElementBench(
			func() (*backend.Backend[int, int], int) {
				var be backend.Backend[int, int]
				be.Init(n, backend.LFU, 0, 0)

				for i := 0; i < n; i++ {
					be.Store(i, 0)
				}

				return &be, be.Len()
			},
			func(be *backend.Backend[int, int]) {
				keys := be.Keys()
				runtime.KeepAlive(keys)
			},
		))
	}
}

func BenchmarkBackendGCLRU(b *testing.B) {
	for _, n := range []int{
		1e3,
		1e4,
		1e5,
		1e6,
	} {
		b.Run(fmt.Sprint(n), newTimePerElementBench(
			func() (*backend.Backend[int, int], int) {
				var be backend.Backend[int, int]
				be.Init(n, backend.LRU, 0, 0)

				for i := 0; i < n; i++ {
					be.Store(i, 0)
				}

				return &be, be.Len()
			},
			func(be *backend.Backend[int, int]) {
				keys := be.Keys()
				runtime.KeepAlive(keys)
			},
		))
	}
}

func createSlice[K comparable, V any](n int, valueFn func(*K, *V)) []*backend.Record[K, V] {
	items := make([]*backend.Record[K, V], n)
	for i := 0; i < len(items); i++ {
		items[i] = &backend.Record[K, V]{}
		if valueFn != nil {
			valueFn(&items[i].Key, &items[i].Value)
		}
	}

	return items
}

func createMap[K comparable, V any](n int, valueFn func(*K, *V)) map[K]*backend.Record[K, V] {
	m := make(map[K]*backend.Record[K, V], n)
	for i := 0; i < n; i++ {
		key := *new(K)
		value := *new(V)

		if valueFn != nil {
			valueFn(&key, &value)
		}

		m[key] = &backend.Record[K, V]{Value: value}
	}

	return m
}

func newTimePerElementBench[S any](createSubject func() (S, int), iterate func(S)) func(b *testing.B) {
	runtime.GC()

	subject, n := createSubject()

	return func(b *testing.B) {
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			iterate(subject)
		}

		b.StopTimer()

		nsPerElement := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / float64(n)
		b.ReportMetric(nsPerElement, "ns/element")
	}
}
