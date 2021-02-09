package evcache_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
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

var _ = Describe("fetching values", func() {
	var (
		evicted chan interface{}
		c       *evcache.Cache
	)

	BeforeEach(func() {
		evicted = make(chan interface{}, 1)
		c = evcache.New().
			WithEvictionCallback(func(key, _ interface{}) {
				defer GinkgoRecover()
				select {
				case evicted <- key:
				default:
					Fail("expected only 1 eviction")
				}
			}).
			Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("key exists", func() {
		BeforeEach(func() {
			value, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
				return "value", nil
			})
			Expect(err).To(BeNil())
			closer.Close()
			Expect(value).To(Equal("value"))

			Expect(c.Len()).To(Equal(1))
		})

		Specify("value is returned", func() {
			value, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
				panic("unexpected fetch")
			})
			Expect(err).To(BeNil())
			closer.Close()
			Expect(value).To(Equal("value"))

			Expect(c.Len()).To(Equal(1))
		})
	})

	When("callback blocks", func() {
		var (
			done         uint64
			valueCh      chan string
			fetchStarted chan struct{}
			wg           sync.WaitGroup
		)
		BeforeEach(func() {
			done = 0
			valueCh = make(chan string, 1)
			fetchStarted = make(chan struct{}, 1)
			wg = sync.WaitGroup{}

			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
					fetchStarted <- struct{}{}
					v := <-valueCh
					if !atomic.CompareAndSwapUint64(&done, 0, 1) {
						Fail("expected to return first")
					}
					return v, nil
				})
				closer.Close()
			}()
		})

		Specify("accessing the same key will block", func() {
			<-fetchStarted

			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()

				// Make sure Get blocks on FetchCallback.
				v, closer, exists := c.Get("key")
				if atomic.CompareAndSwapUint64(&done, 0, 1) {
					Fail("expected first Fetch to have returned first")
				}
				Expect(exists).To(BeTrue())
				closer.Close()
				Expect(v).To(Equal("value"))
			}()

			time.AfterFunc(4*evcache.SyncInterval, func() {
				valueCh <- "value"
			})

			// Make sure Fetch blocks on FetchCallback.
			v, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
				panic("unexpected fetch callback")
			})
			if atomic.CompareAndSwapUint64(&done, 0, 1) {
				Fail("expected first Fetch to have returned first")
			}
			Expect(err).To(BeNil())
			closer.Close()
			Expect(v).To(Equal("value"))

			wg.Wait()
		})

		Specify("autoexpiry keeps working", func() {
			<-fetchStarted

			// Verify other keys can expire.
			v, closer, err := c.Fetch("key1", 4*evcache.SyncInterval, func() (interface{}, error) {
				return "value1", nil
			})
			Expect(err).To(BeNil())
			closer.Close()
			Expect(v).To(Equal("value1"))
			Expect(c.Len()).To(Equal(1))

			Expect(<-evicted).To(Equal("key1"))
			Expect(c.Len()).To(BeZero())

			// Finally send a value and unblock the first key.
			valueCh <- "value"
			v, closer, exists := c.Get("key")
			if atomic.CompareAndSwapUint64(&done, 0, 1) {
				Fail("expected first Fetch to have returned first")
			}
			Expect(exists).To(BeTrue())
			closer.Close()
			Expect(v).To(Equal("value"))
			Expect(c.Len()).To(Equal(1))
		})
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
			// Stores are synchronous.
			_, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
				return "value", nil
			})
			closer.Close()
			Expect(c.Len()).To(Equal(1))

			// Deletes are asynchronous.
			c.Evict("key")
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
		ttl := 4 * evcache.SyncInterval
		_, closer, _ := c.Fetch("key", ttl, func() (interface{}, error) {
			return "value", nil
		})
		closer.Close()

		Eventually(func() int {
			return c.Len()
		}).Should(BeZero())
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

		Expect(c.Len()).To(BeZero())
	})
})

var _ = Describe("ranging over values", func() {
	var (
		evicted chan interface{}
		c       *evcache.Cache
	)

	BeforeEach(func() {
		evicted = make(chan interface{})
		c = evcache.New().
			WithEvictionCallback(func(key, _ interface{}) {
				defer GinkgoRecover()
				evicted <- key
			}).
			Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("callback does not block record", func() {
		_, closer, _ := c.Fetch("key", 100*time.Millisecond, func() (interface{}, error) {
			return "value", nil
		})
		closer.Close()
		Expect(c.Len()).To(Equal(1))

		c.Range(func(key, value interface{}) bool {
			Expect(key).To(Equal("key"))
			Expect(<-evicted).To(Equal("key"))
			Expect(c.Len()).To(BeZero())
			return true
		})
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

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("Fetch causes an overflow", func() {
		Specify("eventually overflowed records are evicted", func() {
			for i := 0; i < n+overflow; i++ {
				_, closer, _ := c.Fetch(i, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
				Expect(c.Len()).To(BeNumerically("<=", n), "capacity cannot be exceeded")
			}

			Eventually(func() uint64 {
				return atomic.LoadUint64(&evicted)
			}).Should(Equal(uint64(overflow)))
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
					Expect(c.Len()).To(BeNumerically("<=", n), "capacity cannot be exceeded")
				}()
			}

			wg.Wait()

			Eventually(func() uint64 {
				return atomic.LoadUint64(&evicted)
			}).Should(Equal(uint64(overflow)))
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
		evictedKeys = make(chan uint64, n)
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
				closer.Close()
				// Make its hit count reflect the key
				// in reverse order so we know the cache
				// will sort the keys back.
				for j := 1; j <= n-i; j++ {
					_, closer, exists := c.Get(k)
					Expect(exists).To(BeTrue())
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
			warmup()

			time.Sleep(2 * evcache.SyncInterval)

			keys := overflow()
			fmt.Printf("near-decreasing order: %v\n", keys)

			sortedness := calcSortedness(sort.Reverse(sort.IntSlice(keys)))
			fmt.Printf("reverse-sortedness: %f\n", sortedness)
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
