package evcache_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

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

var _ = Describe("setting values", func() {
	var c *evcache.Cache

	BeforeEach(func() {
		c = evcache.New().Build()
		fmt.Println("setting values")
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("value exists", func() {
		Specify("it is overwritten", func() {
			c.Set("key", "value", 0)
			Expect(c.Len()).To(Equal(1))

			c.Set("key", "newValue", 0)
			Expect(c.Len()).To(Equal(1))

			value, closer, exists := c.Get("key")
			Expect(exists).To(BeTrue())
			closer.Close()
			Expect(value).To(Equal("newValue"))
			Expect(c.Len()).To(Equal(1))
		})
	})
})

var _ = Describe("fetching values", func() {
	var c *evcache.Cache

	BeforeEach(func() {
		c = evcache.New().Build()
		fmt.Println("fetching values")
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("value exists", func() {
		Specify("it is returned", func() {
			c.Set("key", "value", 0)
			value, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
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
			value, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
				return "value", nil
			})
			Expect(err).To(BeNil())
			closer.Close()
			Expect(value).To(Equal("value"))
			Expect(c.Len()).To(Equal(1))

			value, closer, exists := c.Get("key")
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
		fmt.Println("evicting values")
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("value exists", func() {
		Specify("it is evicted", func() {
			c.Set("key", "value", 0)
			Expect(c.Len()).To(Equal(1))

			value, exists := c.Evict("key")
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
		fmt.Println("flushing")
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("the cache is flushed", func() {
		Specify("all records are evicted", func() {
			c.Set("key", "value", 0)
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

		fmt.Println("autoexp")
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("value with non-zero TTL will be evicted", func() {
		ttl := 4 * evcache.SyncInterval
		c.Set("key", "value", ttl)

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
		fmt.Println("eviction callback")
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("callback waits for closers to be closed", func() {
		key := uint64(0)

		// Fetch a key and keep it alive by not closing the closer.
		_, closer, _ := c.Fetch("key", time.Nanosecond, func() (interface{}, error) {
			return atomic.AddUint64(&key, 1), nil
		})

		Eventually(func() uint64 {
			value, closer, _ := c.Fetch("key", 10*time.Millisecond, func() (interface{}, error) {
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
		fmt.Println("Fetch fails")
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
				val, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
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

var _ = Describe("repeatedly setting the same key", func() {
	var (
		n       = 1000
		evicted uint64
		c       *evcache.Cache
	)

	BeforeEach(func() {
		c = evcache.New().
			WithEvictionCallback(func(_, _ interface{}) {
				defer GinkgoRecover()
				atomic.AddUint64(&evicted, 1)
			}).
			Build()
		fmt.Println("repeatedly")
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("old value is evicted", func() {
		wg := sync.WaitGroup{}
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				c.Set("key", "value", 0)
			}()
		}

		wg.Wait()

		Eventually(func() int {
			// Len can be temporarily 0 during Set.
			return c.Len()
		}).Should(Equal(1))

		Eventually(func() uint64 {
			return atomic.LoadUint64(&evicted)
		}).Should(Equal(uint64(n - 1))) // 1 value stays in the cache.
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
		fmt.Println("overflow")
	})

	When("Set causes an overflow", func() {
		Specify("eventually overflowed records are evicted", func() {
			for i := 0; i < n+overflow; i++ {
				c.Set(i, nil, 0)
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
				_, closer, _ := c.Fetch(i, 0, func() (interface{}, error) {
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
						<-sem
					}()

					cb(i)
					overflow := c.Len() - n
					Expect(overflow).To(BeNumerically("<=", concurrency), "overflow cannot exceed concurrency+1")
					if overflow > 1 {
						// panic(overflow)
						// fmt.Println("overflow", overflow)
					}

					// Make it racy to verify the invariant.
					time.Sleep(time.Duration(rand.Intn(20)) * time.Microsecond)

					// Randomly evict keys.
					if rand.Float64() < 0.6 {
						if _, ok := c.Evict(rand.Intn(i + 1)); ok {
							atomic.AddUint64(&deleted, 1)
						}
					}
				}()
			}

			wg.Wait()

			for i := 0; i < int(deleted); i++ {
				c.Set(i, nil, 0)
			}

			c.Close()
			// TODO this sometimes fails with MaxUint32
			Expect(c.Len()).To(BeZero())

			Eventually(func() uint64 {
				return atomic.LoadUint64(evicted)
			}).Should(Equal(uint64(n+overflow) + deleted))
		},
		Entry(
			"Set",
			func(i int) {
				c.Set(i, nil, 0)
			},
		),
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

//var _ = FDescribe("eventual overflow eviction order", func() {
//var (
//n           = 100
//key         uint64
//evictedKeys chan uint64
//c           *evcache.Cache
//)

//BeforeEach(func() {
//// TODO cap is n
//evictedKeys = make(chan uint64, 10000)
//key = 0
//c = evcache.New().
//WithEvictionCallback(func(key, _ interface{}) {
//defer GinkgoRecover()
//evictedKeys <- key.(uint64)
//}).
//WithCapacity(uint32(n)).
//Build()
//})

//AfterEach(func() {
//c.Close()
//Expect(c.Len()).To(BeZero())
//})

//When("records have different popularity", func() {
//BeforeEach(func() {
//for i := 1; i <= n; i++ {
//k := atomic.AddUint64(&key, 1)
//c.Set(k, nil, 0)
//// Make its hit count reflect the key
//// in reverse order.
//for j := 1; j <= n-i; j++ {
//_, closer, exists := c.Get(k)
//Expect(exists).To(BeTrue())
//closer.Close()
//}
//// time.Sleep(evcache.EvictionInterval)
//}

//// time.Sleep(time.Second)
//})

//Specify("the eviction order is sorted by key hit count", func() {
//var keys []int
//for i := 0; i < n; i++ {
//// Overflow the cache and catch the evicted keys.
//k := atomic.AddUint64(&key, 1)
//c.Set(k, nil, 0)
//keys = append(keys, int(<-evictedKeys))
//fmt.Println(i)
//}

//reverseSorted := sort.IsSorted(sort.Reverse(sort.IntSlice(keys)))
//// if !reverseSorted {
////spew.Dump(keys)
////}
//// TODO This failed
//_ = reverseSorted
//// spew.Dump(keys)
//Expect(reverseSorted).To(BeTrue())
//})
//})
//})
