package evcache_test

import (
	"errors"
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
	evcache.EvictionInterval = 10 * time.Millisecond
})

var _ = Describe("setting values", func() {
	var c *evcache.Cache

	BeforeEach(func() {
		c = evcache.New().Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("value exists", func() {
		Specify("it is overwritten", func() {
			replaced := c.Set("key", "value", 0)
			Expect(replaced).To(BeFalse())
			Expect(c.Len()).To(Equal(1))

			replaced = c.Set("key", "newValue", 0)
			Expect(replaced).To(BeTrue())
			Expect(c.Len()).To(Equal(1))

			value, closer, err := c.Get("key")
			Expect(err).To(BeNil())
			defer closer.Close()
			Expect(value).To(Equal("newValue"))
		})
	})
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
			replaced := c.Set("key", "value", 0)
			Expect(replaced).To(BeFalse())

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
			defer closer.Close()
			Expect(value).To(Equal("value"))

			value, closer, err = c.Get("key")
			Expect(err).To(BeNil())
			defer closer.Close()
			Expect(value).To(Equal("value"))

			Expect(c.Len()).To(Equal(1))
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
			replaced := c.Set("key", "value", 0)
			Expect(replaced).To(BeFalse())
			Expect(c.Len()).To(Equal(1))

			c.Flush()
			Expect(c.Len()).To(Equal(0))
		})
	})
})

var _ = Describe("autoexpiry", func() {
	var (
		once    sync.Once
		evicted chan bool
		c       *evcache.Cache
	)

	BeforeEach(func() {
		once = sync.Once{}
		evicted = make(chan bool)
		c = evcache.New().
			OnExpiry(func(_, _ interface{}) (evict bool) {
				defer GinkgoRecover()
				evict = true
				once.Do(func() {
					// Extend on the first expire.
					evict = false
				})
				return
			}).
			AfterEviction(func(_, _ interface{}) {
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
		ttl := 15 * time.Millisecond
		c.Set("key", "value", ttl)

		start := time.Now()
		<-evicted
		dur := time.Since(start)

		// Add a grace duration for monotonic
		// and wall clock differences.
		Expect(dur + time.Millisecond).To(BeNumerically(">=", 2*ttl))

		Eventually(func() int {
			return c.Len()
		}).Should(BeZero())
	})
})

var _ = Describe("expiry callback", func() {
	var (
		expiry chan struct{}
		c      *evcache.Cache
	)

	BeforeEach(func() {
		expiry = make(chan struct{})
		c = evcache.New().
			OnExpiry(func(_, _ interface{}) (evict bool) {
				defer GinkgoRecover()
				expiry <- struct{}{}
				return true
			}).
			Build()

		// set a record and let it expire.
		c.Set("key", "value", time.Nanosecond)
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("expiry callback blocks", func() {
		Specify("the key can be read", func() {
			value, closer, err := c.Get("key")
			Expect(err).To(BeNil())
			closer.Close()
			Expect(value).To(Equal("value"))

			value, closer, err = c.Fetch("key", 0, func() (interface{}, error) {
				panic("did not expect fetch")
			})
			Expect(err).To(BeNil())
			closer.Close()
			Expect(value).To(Equal("value"))

			<-expiry

			Eventually(func() int {
				return c.Len()
			}).Should(BeZero())
		})

		Specify("the key can't be written", func() {
			// Wait for eviction cycle to have run.
			time.Sleep(2 * evcache.EvictionInterval)

			// After 2 cycles unblock the expiry callback,
			// releasing the key.
			time.AfterFunc(2*evcache.EvictionInterval, func() {
				<-expiry
			})

			// Set should now block until key released
			// and we can set a new value.
			start := time.Now()
			replaced := c.Set("key", "value", 0)
			end := time.Since(start)
			Expect(replaced).To(BeFalse())

			// Add a grace duration for monotonic
			// and wall clock differences.
			Expect(end + time.Millisecond).To(BeNumerically(">=", 2*evcache.EvictionInterval))
		})
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
			AfterEviction(func(_, value interface{}) {
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
		n = 1000
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

var _ = Describe("repeatedly setting the same key", func() {
	var (
		n       = 1000
		evicted uint64
		c       *evcache.Cache
	)

	BeforeEach(func() {
		c = evcache.New().
			AfterEviction(func(_, _ interface{}) {
				defer GinkgoRecover()
				atomic.AddUint64(&evicted, 1)
			}).
			Build()
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
				Expect(c.Len()).To(Equal(1))
			}()
		}

		wg.Wait()

		Eventually(func() int {
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
		overflow = 1000
		evicted  uint64
		c        *evcache.Cache
	)

	BeforeEach(func() {
		evicted = 0
		c = evcache.New().
			AfterEviction(func(_, _ interface{}) {
				defer GinkgoRecover()
				atomic.AddUint64(&evicted, 1)
			}).
			Capacity(uint32(n)).
			Build()
	})

	When("Set causes an overflow", func() {
		Specify("eventually overflowed records are evicted", func() {
			for i := 0; i < n+overflow; i++ {
				c.Set(i, nil, 0)
				Expect(c.Len()).To(BeNumerically("<=", n+1))
			}

			c.Close()
			Expect(c.Len()).To(BeZero())
			Expect(evicted).To(Equal(uint64(n + overflow)))
		})
	})

	When("Fetch causes an overflow", func() {
		Specify("eventually overflowed records are evicted", func() {
			for i := 0; i < n+overflow; i++ {
				_, closer, _ := c.Fetch(i, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
				Expect(c.Len()).To(BeNumerically("<=", n+1))
			}

			c.Close()
			Expect(c.Len()).To(BeZero())
			Expect(evicted).To(Equal(uint64(n + overflow)))
		})
	})

	DescribeTable(
		"concurrent overflow",
		func(cb func(int)) {
			var (
				wg          sync.WaitGroup
				deleted     uint64
				concurrency = 64
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
					Expect(c.Len()-n).To(BeNumerically("<=", concurrency), "overflow cannot exceed concurrency")

					// Randomly evict keys.
					if rand.Float64() < 0.5 {
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
			Expect(c.Len()).To(BeZero())
			Expect(evicted).To(Equal(uint64(n+overflow) + deleted))
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
				_, closer, _ := c.Fetch(i, 0, func() (interface{}, error) {
					return nil, nil
				})
				closer.Close()
			},
		),
	)
})

var _ = Describe("eventual overflow eviction order", func() {
	var (
		n            = 100
		warmupRounds = 2
		key          uint64
		evictedKeys  chan uint64
		c            *evcache.Cache
	)

	BeforeEach(func() {
		evictedKeys = make(chan uint64, n*warmupRounds)
		key = 0
		c = evcache.New().
			AfterEviction(func(key, _ interface{}) {
				defer GinkgoRecover()
				evictedKeys <- key.(uint64)
			}).
			Capacity(uint32(n)).
			Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("records have different popularity", func() {
		insert := func() {
			for i := 1; i <= n; i++ {
				k := atomic.AddUint64(&key, 1)
				_ = c.Set(k, nil, 0)
				// Make its hit count reflect the key.
				for j := 1; j <= int(k); j++ {
					_, closer, err := c.Get(k)
					Expect(err).To(BeNil())
					closer.Close()
				}
			}
		}

		BeforeEach(func() {
			// The eviction order becomes 100% ordered
			// after the cache has evicted its full
			// capacity of keys - there has been
			// one turnover.
			for i := 0; i < warmupRounds; i++ {
				insert()
			}

			Eventually(func() int {
				return c.Len()
			}).Should(Equal(n))

			// Drain warmup values (not yet ordered).
			for i := 0; i < n*(warmupRounds-1); i++ {
				<-evictedKeys
			}

			select {
			case <-evictedKeys:
				Fail("did not expect to receive from evicted chan")
			default:
			}
		})

		Specify("the eviction order is sorted by key hit count", func() {
			var prev uint64
			for i := 0; i < n; i++ {
				// Overflow the cache.
				k := atomic.AddUint64(&key, 1)
				c.Set(k, nil, 0)

				evicted := <-evictedKeys
				Expect(evicted).To(BeNumerically(">", prev), "expect LFU order")
				prev = evicted
			}
		})
	})
})
