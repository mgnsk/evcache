package evcache_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/mgnsk/evcache/v4"
	. "github.com/mgnsk/evcache/v4/internal/testing"
)

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
