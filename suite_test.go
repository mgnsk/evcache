package evcache_test

import (
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
			_, closer, _ := c.LoadOrStore(atomic.AddUint64(&key, 1), 0, func() (interface{}, error) {
				return 0, nil
			})
			closer.Close()
		}
	})
}
