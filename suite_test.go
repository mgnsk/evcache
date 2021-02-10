package evcache_test

import (
	"errors"
	"math/rand"
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

func TestCache(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "evcache suite")
}

func BenchmarkLFUContention(b *testing.B) {
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

func BenchmarkFetchAndEvict(b *testing.B) {
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
