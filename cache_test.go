package evcache_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/mgnsk/evcache/v4"
)

func TestCacheGoGC(t *testing.T) {
	capacity := 1_000_000
	c := evcache.New[int, byte](evcache.WithCapacity(capacity))

	for i := range capacity {
		c.StoreTTL(i, 0, time.Hour) // Store with TTL to trigger the cleanup runner.
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
