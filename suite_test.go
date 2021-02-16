package evcache_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgnsk/evcache"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func wait(wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		Fail("test timed out")
	}
}

func TestCache(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "evcache suite")
}

func BenchmarkCapacityParallel(b *testing.B) {
	c := evcache.New().WithCapacity(2).Build()
	var key uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, closer, _ := c.Fetch(atomic.AddUint64(&key, 1), 0, func() (interface{}, error) {
				return 0, nil
			})
			closer.Close()
		}
	})
}

func BenchmarkFetchAndEvictParallel(b *testing.B) {
	c := evcache.New().Build()
	index := uint64(0)
	errFetch := errors.New("error fetching")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if idx := atomic.AddUint64(&index, 1); idx%2 == 0 {
				_, closer, _ := c.Fetch(0, 0, func() (interface{}, error) {
					if idx%4 == 0 {
						return nil, errFetch
					}
					return 0, nil
				})
				if closer != nil {
					closer.Close()
				}
			} else {
				c.Evict(0)
			}
		}
	})
}

func BenchmarkGet(b *testing.B) {
	c := evcache.New().Build()
	for i := 0; i < b.N; i++ {
		c.Set(i, nil, 0)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, closer, _ := c.Get(i)
		closer.Close()
	}
}

func BenchmarkSetNotExists(b *testing.B) {
	c := evcache.New().Build()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set(i, nil, 0)
	}
}

func BenchmarkSetExists(b *testing.B) {
	c := evcache.New().Build()
	for i := 0; i < b.N; i++ {
		c.Set(0, nil, 0)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set(0, nil, 0)
	}
}

func BenchmarkFetchExists(b *testing.B) {
	c := evcache.New().Build()
	for i := 0; i < b.N; i++ {
		c.Set(i, nil, 0)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, closer, _ := c.Fetch(i, 0, func() (interface{}, error) {
			panic("unexpected fetch callback")
		})
		closer.Close()
	}
}

func BenchmarkFetchNotExists(b *testing.B) {
	c := evcache.New().Build()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, closer, _ := c.Fetch(i, 0, func() (interface{}, error) {
			return nil, nil
		})
		closer.Close()
	}
}

func BenchmarkPop(b *testing.B) {
	c := evcache.New().Build()
	for i := 0; i < b.N; i++ {
		c.Set(i, nil, 0)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Pop()
	}
}
