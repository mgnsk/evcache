package evcache_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/mgnsk/evcache"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
)

var _ = BeforeSuite(func() {
	rand.Seed(time.Now().UnixNano())
	// Reduce the interval to make tests run faster
	// and use gomega.Eventually with default timeout.
	evcache.SyncInterval = 10 * time.Millisecond
})

var _ = Describe("fetching values", func() {
	var c *evcache.Cache

	BeforeEach(func() {
		c = evcache.New().Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("value exists", func() {
		Specify("it is returned", func() {
			c.LoadOrStore("key", 0, func() (interface{}, error) {
				return "value", nil
			})
			value, closer, err := c.LoadOrStore("key", 0, func() (interface{}, error) {
				panic("unexpected fetch")
			})
			Expect(err).To(BeNil())
			defer closer.Close()
			Expect(value).To(Equal("value"))

			Expect(c.Len()).To(Equal(1))
		})
	})

	When("the value does not exist", func() {
		Specify("it is created", func() {
			value, closer, err := c.LoadOrStore("key", 0, func() (interface{}, error) {
				return "value", nil
			})
			Expect(err).To(BeNil())
			closer.Close()
			Expect(value).To(Equal("value"))
			Expect(c.Len()).To(Equal(1))

			value, closer, exists := c.Load("key")
			Expect(exists).To(BeTrue())
			closer.Close()
			Expect(value).To(Equal("value"))
			Expect(c.Len()).To(Equal(1))
		})
	})
})

var _ = Describe("evicting values", func() {
	var c *evcache.Cache

	BeforeEach(func() {
		c = evcache.New().Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("value exists", func() {
		Specify("it is evicted", func() {
			_, closer, _ := c.LoadOrStore("key", 0, func() (interface{}, error) {
				return "value", nil
			})
			closer.Close()
			Expect(c.Len()).To(Equal(1))

			value, exists := c.LoadAndDelete("key")
			Expect(exists).To(BeTrue())
			Expect(value).To(Equal("value"))
			Expect(c.Len()).To(BeZero())
		})
	})
})

var _ = Describe("flushing the cache", func() {
	var c *evcache.Cache

	BeforeEach(func() {
		c = evcache.New().Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("the cache is flushed", func() {
		Specify("all records are evicted", func() {
			_, closer, _ := c.LoadOrStore("key", 0, func() (interface{}, error) {
				return "value", nil
			})
			closer.Close()
			Expect(c.Len()).To(Equal(1))

			c.Flush()
			Expect(c.Len()).To(Equal(0))
		})
	})
})

var _ = Describe("autoexpiry", func() {
	var (
		evicted chan bool
		c       *evcache.Cache
	)

	BeforeEach(func() {
		evicted = make(chan bool)
		c = evcache.New().
			WithEvictionCallback(func(_, _ interface{}) {
				defer GinkgoRecover()
				close(evicted)
			}).
			Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("value with non-zero TTL will be evicted", func() {
		ttl := 4 * evcache.SyncInterval
		_, closer, _ := c.LoadOrStore("key", ttl, func() (interface{}, error) {
			return "value", nil
		})
		closer.Close()

		start := time.Now()
		<-evicted

		Eventually(func() int {
			return c.Len()
		}).Should(BeZero())

		_ = start
		// Expect(time.Since(start)).To(BeNumerically(">=", 2*evcache.EvictionInterval))
	})
})

var _ = Describe("eviction callback", func() {
	var (
		evicted chan uint64
		c       *evcache.Cache
	)

	BeforeEach(func() {
		evicted = make(chan uint64, 2)
		c = evcache.New().
			WithEvictionCallback(func(_, value interface{}) {
				defer GinkgoRecover()
				evicted <- value.(uint64)
			}).
			Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("callback waits for closers to be closed", func() {
		// Skip("not ready yet")
		key := uint64(0)

		// when its thhe same key, the waitgroup gets broken???
		// but they should be different records?
		// does the waitgroup counter get stuck at 1?? ie no Done is called?

		// Fetch a key and keep it alive by not closing the closer.
		_, closer1, _ := c.LoadOrStore("key", time.Nanosecond, func() (interface{}, error) {
			return atomic.AddUint64(&key, 1), nil
		})

		// time.Sleep(100 * time.Millisecond)

		Eventually(func() uint64 {
			value, closer2, _ := c.LoadOrStore("key", 10*time.Millisecond, func() (interface{}, error) {
				return atomic.AddUint64(&key, 1), nil
			})
			Expect(closer2).NotTo(BeNil())
			//if closer1 == closer2 {
			//fmt.Println("got old")
			//return 0
			//} else {
			//fmt.Println("got new")
			//}
			closer2.Close()
			return value.(uint64)
		}).Should(Equal(uint64(2)))

		// we made it here but not evicting

		// Second value before first value.
		// fmt.Println("evicted1", <-evicted)
		// fmt.Println("evicted2", <-evicted)
		Expect(<-evicted).To(Equal(uint64(2)))
		closer1.Close()
		Expect(<-evicted).To(Equal(uint64(1)))

		Eventually(func() int {
			return c.Len()
		}).Should(BeZero())
	})
})

var _ = Describe("Fetch fails with an error", func() {
	var (
		n = 10
		c *evcache.Cache
	)

	BeforeEach(func() {
		c = evcache.New().Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("no records are cached", func() {
		wg := sync.WaitGroup{}
		errFetch := errors.New("error creating value")

		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				val, closer, err := c.LoadOrStore("key", 0, func() (interface{}, error) {
					return nil, errFetch
				})
				Expect(errors.Is(err, errFetch)).To(BeTrue())
				Expect(closer).To(BeNil())
				Expect(val).To(BeNil())
				Expect(c.Len()).To(BeNumerically("<=", 1))
			}()
		}

		wg.Wait()
	})
})

var _ = Describe("overflow when setting values", func() {
	var (
		n        = 10
		overflow = 5
		evicted  *uint64
		c        *evcache.Cache
	)

	newCache := func(counter *uint64) *evcache.Cache {
		return evcache.New().
			WithEvictionCallback(func(_, _ interface{}) {
				defer GinkgoRecover()
				atomic.AddUint64(counter, 1)
			}).
			WithCapacity(uint32(n)).
			Build()
	}

	BeforeEach(func() {
		// TODO why we use pointer? test hsould finish before going forwardjh
		evicted = new(uint64)
		c = newCache(evicted)
	})

	When("Set causes an overflow", func() {
		Specify("eventually overflowed records are evicted", func() {
			for i := 0; i < n+overflow; i++ {
				_, closer, _ := c.LoadOrStore(i, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
				Expect(c.Len()).To(BeNumerically("<=", n+overflow))
			}

			c.Close()
			Expect(c.Len()).To(BeZero())
			Eventually(func() uint64 {
				return atomic.LoadUint64(evicted)
			}).Should(Equal(uint64(n + overflow)))
		})
	})

	When("Fetch causes an overflow", func() {
		Specify("eventually overflowed records are evicted", func() {
			for i := 0; i < n+overflow; i++ {
				_, closer, _ := c.LoadOrStore(i, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
				Expect(c.Len()).To(BeNumerically("<=", n+overflow))
			}

			c.Close()
			Expect(c.Len()).To(BeZero())
			Eventually(func() uint64 {
				return atomic.LoadUint64(evicted)
			}).Should(Equal(uint64(n + overflow)))
		})
	})

	DescribeTable(
		"concurrent overflow",
		func(cb func(int)) {
			var (
				wg          sync.WaitGroup
				deleted     uint64
				concurrency = 4
			)

			sem := make(chan struct{}, concurrency)

			for i := 0; i < n+overflow; i++ {
				i := i
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					defer func() {
						select {
						case <-sem:
						default:
						}
					}()

					cb(i)
					overflow := c.Len() - n
					Expect(overflow).To(BeNumerically("<=", concurrency+1), "overflow cannot exceed concurrency+1")
					if overflow > 1 {
						// panic(overflow)
						// fmt.Println("overflow", overflow)
					}

					// Randomly evict keys.
					//if rand.Float64() < 0.6 {
					//if _, ok := c.LoadAndDelete(rand.Intn(i + 1)); ok {
					//atomic.AddUint64(&deleted, 1)
					//}
					//}
				}()
			}

			wg.Wait()

			for i := 0; i < int(deleted); i++ {
				_, closer, _ := c.LoadOrStore(i, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
			}

			c.Close()
			// TODO this sometimes fails with MaxUint32
			Expect(c.Len()).To(BeZero())

			Eventually(func() uint64 {
				return atomic.LoadUint64(evicted)
			}).Should(Equal(uint64(n+overflow) + deleted))
		},
		Entry(
			"Fetch",
			func(i int) {
				value, closer, err := c.LoadOrStore(i, 0, func() (interface{}, error) {
					return "value", nil
				})
				Expect(err).To(BeNil())
				Expect(value).To(Equal("value"))
				closer.Close()
			},
		),
	)
})

var _ = Describe("eventual overflow eviction order", func() {
	var (
		n           = 100
		key         uint64
		evictedKeys chan uint64
		c           *evcache.Cache
	)

	BeforeEach(func() {
		// TODO cap is n
		evictedKeys = make(chan uint64, 10000)
		key = 0
		c = evcache.New().
			WithEvictionCallback(func(key, _ interface{}) {
				defer GinkgoRecover()
				evictedKeys <- key.(uint64)
			}).
			WithCapacity(uint32(n)).
			Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("records have different popularity", func() {
		BeforeEach(func() {
			for i := 1; i <= n; i++ {
				k := atomic.AddUint64(&key, 1)
				_, closer, _ := c.LoadOrStore(k, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
				// Make its hit count reflect the key
				// in reverse order.
				for j := 1; j <= n-i; j++ {
					_, closer, exists := c.Load(k)
					Expect(exists).To(BeTrue())
					closer.Close()
				}
			}

			// time.Sleep(time.Second)
		})

		Specify("the eviction order is sorted by key hit count", func() {
			// Skip("TODO")
			var keys []int
			for i := 0; i < n; i++ {
				// Overflow the cache and catch the evicted keys.
				k := atomic.AddUint64(&key, 1)
				_, closer, _ := c.LoadOrStore(k, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
				evictedKey := int(<-evictedKeys)
				fmt.Println(evictedKey)
				keys = append(keys, evictedKey)
				// fmt.Println()
			}

			reverseSorted := sort.IsSorted(sort.Reverse(sort.IntSlice(keys)))
			// if !reverseSorted {
			spew.Dump(keys)
			//}
			// TODO This failed
			_ = reverseSorted
			// spew.Dump(keys)
			Expect(reverseSorted).To(BeTrue())
		})
	})
})
