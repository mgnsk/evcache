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

		value, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
			return "value", nil
		})
		Expect(err).To(BeNil())
		closer.Close()
		Expect(value).To(Equal("value"))

		Expect(c.Len()).To(Equal(1))
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("value is returned", func() {
		fmt.Println(1)

		value, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
			panic("unexpected fetch")
		})
		Expect(err).To(BeNil())
		closer.Close()
		Expect(value).To(Equal("value"))

		Expect(c.Len()).To(Equal(1))
	})
})

var _ = Describe("deleting values", func() {
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
			fmt.Println(2)
			// Stores are synchronous.
			_, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
				return "value", nil
			})
			closer.Close()
			Expect(c.Len()).To(Equal(1))

			// Deletes are asynchronous.
			c.Delete("key")
			Eventually(func() int {
				return c.Len()
			}).Should(BeZero())
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
			fmt.Println(3)
			_, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
				return "value", nil
			})
			closer.Close()
			Expect(c.Len()).To(Equal(1))

			c.Flush()

			Eventually(func() int {
				return c.Len()
			}).Should(BeZero())
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
		fmt.Println(4)
		ttl := 4 * evcache.SyncInterval
		_, closer, _ := c.Fetch("key", ttl, func() (interface{}, error) {
			return "value", nil
		})
		closer.Close()

		start := time.Now()
		<-evicted

		Eventually(func() int {
			return c.Len()
		}).Should(BeZero())

		_ = start
		// TODO
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
		fmt.Println(5)
		key := uint64(0)

		// Fetch a key and keep it alive by not closing the closer.
		_, closer, _ := c.Fetch("key", time.Nanosecond, func() (interface{}, error) {
			// time.Sleep(100 * time.Millisecond)
			return atomic.AddUint64(&key, 1), nil
		})

		Eventually(func() uint64 {
			value, closer, _ := c.Fetch("key", 10*time.Millisecond, func() (interface{}, error) {
				time.Sleep(20 * time.Microsecond)
				return atomic.AddUint64(&key, 1), nil
			})
			Expect(closer).NotTo(BeNil())
			closer.Close()
			return value.(uint64)
		}).Should(Equal(uint64(2)))

		// Second value before first value.
		Expect(<-evicted).To(Equal(uint64(2)))
		closer.Close()
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
		fmt.Println(6)
		wg := sync.WaitGroup{}
		errFetch := errors.New("error creating value")

		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				val, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
					return nil, errFetch
				})
				Expect(errors.Is(err, errFetch)).To(BeTrue())
				Expect(closer).To(BeNil())
				Expect(val).To(BeNil())
				Expect(c.Len()).To(BeZero())
			}()
		}

		wg.Wait()
	})
})

var _ = Describe("overflow when setting values", func() {
	var (
		n        = 10
		overflow = 5
		evicted  uint64
		c        *evcache.Cache
	)

	BeforeEach(func() {
		evicted = 0
		c = evcache.New().
			WithEvictionCallback(func(_, _ interface{}) {
				defer GinkgoRecover()
				atomic.AddUint64(&evicted, 1)
			}).
			WithCapacity(uint32(n)).
			Build()
	})

	When("Set causes an overflow", func() {
		Specify("eventually overflowed records are evicted", func() {
			fmt.Println(7)
			for i := 0; i < n+overflow; i++ {
				_, closer, _ := c.Fetch(i, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
				Expect(c.Len()).To(BeNumerically("<=", n+overflow))
			}

			c.Close()
			Expect(c.Len()).To(BeZero())
			Eventually(func() uint64 {
				return atomic.LoadUint64(&evicted)
			}).Should(Equal(uint64(n + overflow)))
		})
	})

	When("Fetch causes an overflow", func() {
		Specify("eventually overflowed records are evicted", func() {
			fmt.Println(8)
			for i := 0; i < n+overflow; i++ {
				_, closer, _ := c.Fetch(i, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
				Expect(c.Len()).To(BeNumerically("<=", n+overflow))
			}

			c.Close()
			Expect(c.Len()).To(BeZero())
			Eventually(func() uint64 {
				return atomic.LoadUint64(&evicted)
			}).Should(Equal(uint64(n + overflow)))
		})
	})

	DescribeTable(
		"concurrent overflow",
		func(cb func(int)) {
			var (
				wg          sync.WaitGroup
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

					// Randomly evict keys.
					if rand.Float64() < 0.6 {
						c.Delete(rand.Intn(i + 1))
					}
				}()
			}

			wg.Wait()

			c.Close()
			Expect(c.Len()).To(BeZero())

			Eventually(func() uint64 {
				return atomic.LoadUint64(&evicted)
			}).Should(Equal(uint64(n + overflow)))
		},
		Entry(
			"Fetch",
			func(i int) {
				value, closer, err := c.Fetch(i, 0, func() (interface{}, error) {
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
		n           = 10
		key         uint64
		evictedKeys chan uint64
		c           *evcache.Cache
	)

	BeforeEach(func() {
		evictedKeys = make(chan uint64, 2<<16)
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
		warmup := func() {
			for i := 1; i <= n; i++ {
				k := atomic.AddUint64(&key, 1)
				_, closer, _ := c.Fetch(k, 0, func() (interface{}, error) {
					return nil, nil
				})
				time.Sleep(2 * evcache.SyncInterval)
				closer.Close()
				// Make its hit count reflect the key
				// in reverse order.
				for j := 1; j <= n-i; j++ {
					_, closer, exists := c.Load(k)
					Expect(exists).To(BeTrue())
					time.Sleep(2 * evcache.SyncInterval)
					closer.Close()
				}
			}
		}

		overflow := func() (keys []int) {
			for i := 0; i < n; i++ {
				// Overflow the cache and catch the evicted keys.
				k := atomic.AddUint64(&key, 1)
				_, closer, _ := c.Fetch(k, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
				evictedKey := int(<-evictedKeys)
				keys = append(keys, evictedKey)
			}
			return keys
		}

		Specify("the eviction order is almost sorted by key hit count", func() {
			Skip("TODO")
			fmt.Println(9)
			warmup()
			keys := overflow()
			spew.Dump(keys)
			sortedness := calcSortedness(sort.Reverse(sort.IntSlice(keys)))
			spew.Dump(sortedness)
		})
	})
})

func calcSortedness(data sort.Interface) float64 {
	invalid := 0
	n := data.Len()
	for i := n - 1; i > 0; i-- {
		if data.Less(i, i-1) {
			invalid++
		}
	}
	return 1.0 - float64(invalid)/float64(n)
}
