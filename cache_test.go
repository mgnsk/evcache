package evcache_test

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgnsk/evcache/v3"
	. "github.com/onsi/gomega"
)

func TestLoadOrStoreNotExists(t *testing.T) {
	g := NewWithT(t)

	c := evcache.New[string, int](0)

	_, loaded := c.LoadOrStore("key", 0, 1)
	g.Expect(loaded).To(BeFalse())
	g.Expect(c.Exists("key")).To(BeTrue())
	g.Expect(c.Len()).To(Equal(1))
}

func TestLoadOrStoreExists(t *testing.T) {
	g := NewWithT(t)

	c := evcache.New[string, int](0)

	c.LoadOrStore("key", 0, 1)

	v, loaded := c.LoadOrStore("key", 0, 2)
	g.Expect(loaded).To(BeTrue())
	g.Expect(v).To(Equal(1))
}

func TestFetchCallbackBlocks(t *testing.T) {
	c := evcache.New[string, string](0)

	done := make(chan struct{})
	fetchStarted := make(chan struct{})
	defer close(done)

	go func() {
		_, _ = c.Fetch("key", 0, func() (string, error) {
			close(fetchStarted)
			<-done
			return "", nil
		})
	}()

	<-fetchStarted

	t.Run("non-blocking Exists", func(t *testing.T) {
		g := NewWithT(t)

		g.Expect(c.Exists("key")).To(BeFalse())
	})

	t.Run("non-blocking Evict", func(t *testing.T) {
		g := NewWithT(t)

		_, ok := c.Evict("key")
		g.Expect(ok).To(BeFalse())
	})

	t.Run("non-blocking Get", func(t *testing.T) {
		g := NewWithT(t)

		_, ok := c.Get("key")
		g.Expect(ok).To(BeFalse())
	})

	t.Run("autoexpiry for other keys works", func(t *testing.T) {
		g := NewWithT(t)

		c.LoadOrStore("key1", time.Millisecond, "value1")

		g.Eventually(func() bool {
			return c.Exists("key1")
		}).Should(BeFalse())
	})

	t.Run("non-blocking Range", func(t *testing.T) {
		g := NewWithT(t)

		c.LoadOrStore("key1", 0, "value1")

		n := 0
		c.Range(func(key, value string) bool {
			if key == "key" {
				g.Fail("expected to skip key")
			}

			v, ok := c.Evict(key)
			g.Expect(ok).To(BeTrue())
			g.Expect(v).To(Equal(value))
			g.Expect(c.Len()).To(Equal(0))

			n++
			return true
		})

		g.Expect(n).To(Equal(1))
	})
}

func TestFetchCallbackPanic(t *testing.T) {
	g := NewWithT(t)

	c := evcache.New[string, string](0)

	func() {
		defer func() {
			_ = recover()
		}()

		_, _ = c.Fetch("key", 0, func() (string, error) {
			panic("failed")
		})
	}()

	// Fetching again does not deadlock as the uninitialized value was cleaned up.
	v, err := c.Fetch("key", 0, func() (string, error) {
		return "new value", nil
	})

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(v).To(Equal("new value"))
}

func TestConcurrentFetch(t *testing.T) {
	t.Run("returns error", func(t *testing.T) {
		g := NewWithT(t)

		c := evcache.New[string, string](0)

		errCh := make(chan error)
		fetchStarted := make(chan struct{})

		go func() {
			_, _ = c.Fetch("key", 0, func() (string, error) {
				close(fetchStarted)
				return "", <-errCh
			})
		}()

		<-fetchStarted
		errCh <- fmt.Errorf("error fetching value")

		v, err := c.Fetch("key", 0, func() (string, error) {
			return "value", nil
		})

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(v).To(Equal("value"))
	})

	t.Run("returns value", func(t *testing.T) {
		g := NewWithT(t)

		c := evcache.New[string, string](0)

		valueCh := make(chan string)
		fetchStarted := make(chan struct{})

		go func() {
			_, _ = c.Fetch("key", 0, func() (string, error) {
				close(fetchStarted)
				return <-valueCh, nil
			})
		}()

		<-fetchStarted
		valueCh <- "value"

		v, err := c.Fetch("key", 0, func() (string, error) {
			return "value1", nil
		})

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(v).To(Equal("value"))
	})
}

func TestEvict(t *testing.T) {
	g := NewWithT(t)

	c := evcache.New[string, string](0)

	c.LoadOrStore("key", 0, "value")

	v, ok := c.Evict("key")
	g.Expect(ok).To(BeTrue())
	g.Expect(v).To(Equal("value"))
	g.Expect(c.Exists("key")).To(BeFalse())
}

func TestOverflow(t *testing.T) {
	g := NewWithT(t)

	capacity := 100
	c := evcache.New[int, int](capacity)

	for i := 0; i < 2*capacity; i++ {
		c.LoadOrStore(i, 0, 0)
	}

	g.Eventually(c.Len).Should(Equal(capacity))
}

func TestExpire(t *testing.T) {
	g := NewWithT(t)

	c := evcache.New[int, int](0)

	n := 10
	for i := 0; i < n; i++ {
		// Store records in descending TTL order.
		d := time.Duration(n-i) * time.Millisecond
		_, _ = c.LoadOrStore(i, d, 0)
	}

	g.Eventually(c.Len).Should(BeZero())
}

func TestExpireEdgeCase(t *testing.T) {
	g := NewWithT(t)

	c := evcache.New[int, *string](0)

	v1 := new(string)

	c.LoadOrStore(0, 10*time.Millisecond, v1)

	// Wait until v1 expires.
	g.Eventually(c.Len).Should(BeZero())

	// Assert that after v1 expires, v2 with a longer TTL than v1, can expire,
	// specifically that runGC() resets earliestExpireAt to zero,
	// so that LoadOrStore schedules the GC loop.
	v2 := new(string)
	c.LoadOrStore(1, time.Second, v2)

	g.Eventually(c.Len, 2*time.Second).Should(BeZero())
}

func TestOverflowEvictionOrdering(t *testing.T) {
	g := NewWithT(t)

	capacity := 10
	c := evcache.New[int, *string](capacity)
	evictedKeys := make(chan int)

	// Fill the cache.
	for i := 1; i <= capacity; i++ {
		value := new(string)
		i := i
		runtime.SetFinalizer(value, func(any) {
			evictedKeys <- i
		})

		_, loaded := c.LoadOrStore(i, 0, value)
		g.Expect(loaded).To(BeFalse())
		g.Expect(c.Len()).To(Equal(i))
	}

	// Overflow the cache and catch evicted keys.
	var keys []int
	for i := capacity + 1; i <= 2*capacity; i++ {
		_, loaded := c.LoadOrStore(i, 0, nil)
		g.Expect(loaded).To(BeFalse())

		// Run the GC until the value is garbage collected
		// and the value finalizer runs.
		g.Eventually(func() bool {
			runtime.GC()
			select {
			case key := <-evictedKeys:
				keys = append(keys, key)
				return true
			default:
				return false
			}
		}).Should(BeTrue())
	}

	g.Expect(keys).To(HaveLen(capacity))
	g.Expect(sort.IntsAreSorted(keys)).To(BeTrue())
}

func BenchmarkFetchAndEvictParallel(b *testing.B) {
	c := evcache.New[uint64, int](0)
	index := uint64(0)
	errFetch := errors.New("error fetching")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if idx := atomic.AddUint64(&index, 1); idx%2 == 0 {
				_, _ = c.Fetch(0, 0, func() (int, error) {
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

func BenchmarkSyncMapBackendFetchAndEvictParallel(b *testing.B) {
	c := evcache.NewWithBackend(evcache.NewSyncMapBackend[uint64, int](0))
	index := uint64(0)
	errFetch := errors.New("error fetching")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if idx := atomic.AddUint64(&index, 1); idx%2 == 0 {
				_, _ = c.Fetch(0, 0, func() (int, error) {
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
	c := evcache.New[uint64, int](0)
	c.LoadOrStore(0, 0, 0)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Fetch(0, 0, func() (int, error) {
			panic("unexpected fetch callback")
		})
	}
}

func BenchmarkSyncMapBackendFetchExists(b *testing.B) {
	c := evcache.NewWithBackend(evcache.NewSyncMapBackend[uint64, int](0))
	c.LoadOrStore(0, 0, 0)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Fetch(0, 0, func() (int, error) {
			panic("unexpected fetch callback")
		})
	}
}

func BenchmarkFetchNotExists(b *testing.B) {
	c := evcache.New[int, int](0)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Fetch(i, 0, func() (int, error) {
			return 0, nil
		})
	}
}

func BenchmarkSyncMapBackendFetchNotExists(b *testing.B) {
	c := evcache.NewWithBackend(evcache.NewSyncMapBackend[int, int](0))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Fetch(i, 0, func() (int, error) {
			return 0, nil
		})
	}
}
