package evcache_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/mgnsk/evcache/v4"
)

func BenchmarkFetchAndEvictParallel(b *testing.B) {
	b.StopTimer()

	c := evcache.New[uint64, int]()
	index := uint64(0)
	errFetch := errors.New("error fetching")

	b.ReportAllocs()
	b.StartTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if idx := atomic.AddUint64(&index, 1); idx%2 == 0 {
				_, _ = c.Fetch(0, func() (int, error) {
					if idx%4 == 0 {
						return 0, errFetch
					}
					return 0, nil
				})
			} else {
				c.Evict(0)
			}
		}
	})
}

func BenchmarkFetchExists(b *testing.B) {
	b.StopTimer()

	c := evcache.New[uint64, int]()
	c.Fetch(0, func() (int, error) {
		return 0, nil
	})

	b.ReportAllocs()
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		_, _ = c.Fetch(0, func() (int, error) {
			panic("unexpected fetch callback")
		})
	}
}

func BenchmarkFetchNotExists(b *testing.B) {
	b.StopTimer()

	c := evcache.New[int, int]()

	b.ReportAllocs()
	b.StartTimer()

	for i := 0; i < b.N; i++ {
		_, _ = c.Fetch(i, func() (int, error) {
			return 0, nil
		})
	}
}

func BenchmarkLoad(b *testing.B) {
	for _, policy := range []string{
		evcache.FIFO,
		evcache.LRU,
		evcache.LFU,
	} {
		b.Run(policy, func(b *testing.B) {
			c := evcache.New[int, int](
				evcache.WithPolicy(policy),
			)

			c.Store(0, 1)

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				value, _ := c.Load(0)
				if value != 1 {
					b.Fatal("expected value to be loaded")
				}
			}
		})
	}
}
