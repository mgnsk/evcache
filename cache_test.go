package evcache_test

import (
	"errors"
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
	// and use default gomega.Eventually with
	// default timeout of 1s.
	evcache.SyncInterval = 10 * time.Millisecond
})

var _ = Describe("setting values", func() {
	var (
		evicted chan interface{}
		c       *evcache.Cache
	)

	BeforeEach(func() {
		evicted = make(chan interface{}, 2)
		c = evcache.New().
			WithEvictionCallback(func(key, _ interface{}) {
				defer GinkgoRecover()
				select {
				case evicted <- key:
				default:
					Fail("expected only 2 evictions")
				}
			}).
			Build()
	})

	When("key exists", func() {
		BeforeEach(func() {
			c.Set("key", 0, 0)
			Expect(c.Len()).To(Equal(1))
			Expect(c.Exists("key")).To(BeTrue())
		})

		Specify("value is replaced", func() {
			c.Set("key", 1, 0)
			Eventually(func() int {
				return c.Len()
			}).Should(Equal(1))

			c.Close()
			Expect(c.Len()).To(BeZero())
		})
	})
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

			c.Close()
			Expect(c.Len()).To(BeZero())
		})
	})
})

var _ = Describe("Fetch callback", func() {
	var (
		evicted chan interface{}
		c       *evcache.Cache
	)

	BeforeEach(func() {
		evicted = make(chan interface{}, 2)
		c = evcache.New().
			WithEvictionCallback(func(key, _ interface{}) {
				defer GinkgoRecover()
				select {
				case evicted <- key:
				default:
					Fail("expected only 2 evictions")
				}
			}).
			Build()
	})

	When("callback blocks and same key is accessed", func() {
		var (
			done    uint64
			valueCh chan string
		)
		BeforeEach(func() {
			done = 0
			valueCh = make(chan string)

			fetchStarted := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				_, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
					close(fetchStarted)
					v := <-valueCh
					if !atomic.CompareAndSwapUint64(&done, 0, 1) {
						Fail("expected to return first")
					}
					return v, nil
				})
				closer.Close()
			}()
			<-fetchStarted
		})

		Specify("Exists will not block and returns false", func() {
			Expect(c.Exists("key")).To(BeFalse())
			close(valueCh)

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("Get blocks", func() {
			time.AfterFunc(4*evcache.SyncInterval, func() {
				valueCh <- "value"
			})

			v, closer, exists := c.Get("key")
			if atomic.CompareAndSwapUint64(&done, 0, 1) {
				Fail("expected first Fetch to have returned first")
			}
			Expect(exists).To(BeTrue())
			closer.Close()
			Expect(v).To(Equal("value"))

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("Fetch blocks", func() {
			time.AfterFunc(4*evcache.SyncInterval, func() {
				valueCh <- "value"
			})

			v, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
				panic("unexpected fetch callback")
			})
			if atomic.CompareAndSwapUint64(&done, 0, 1) {
				Fail("expected first Fetch to have returned first")
			}
			Expect(err).To(BeNil())
			closer.Close()
			Expect(v).To(Equal("value"))

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("Evict blocks", func() {
			time.AfterFunc(4*evcache.SyncInterval, func() {
				valueCh <- "value"
			})

			v, ok := c.Evict("key")
			if atomic.CompareAndSwapUint64(&done, 0, 1) {
				Fail("expected first Fetch to have returned first")
			}
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal("value"))

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("autoexpiry keeps working", func() {
			// Verify other keys can expire.
			c.Set("key1", "value1", 4*evcache.SyncInterval)
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

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("Range skips the blocking key", func() {
			c.Set("key1", "value1", 0)
			Expect(c.Len()).To(Equal(1))

			n := 0
			c.Range(func(key, value interface{}) bool {
				if key == "key" {
					Fail("expected to skip key")
				}
				c.Evict("key1")
				n++
				return true
			})
			Expect(n).To(Equal(1))

			valueCh <- "value"

			c.Close()
			Expect(c.Len()).To(BeZero())
		})
	})
})

var _ = Describe("deleting values", func() {
	var c *evcache.Cache

	BeforeEach(func() {
		c = evcache.New().Build()
	})

	When("value exists", func() {
		Specify("it is evicted", func() {
			// Stores are synchronous.
			c.Set("key", "value", 0)
			Expect(c.Len()).To(Equal(1))

			c.Evict("key")
			Expect(c.Exists("key")).To(BeFalse())
			// Evicts are synchronous for the map
			// but asynchronous for the list.
			Eventually(func() int {
				return c.Len()
			}).Should(BeZero())

			c.Close()
			Expect(c.Len()).To(BeZero())
		})
	})
})

var _ = Describe("flushing the cache", func() {
	var c *evcache.Cache

	BeforeEach(func() {
		c = evcache.New().Build()
	})

	When("the cache is flushed", func() {
		Specify("all records are evicted", func() {
			c.Set("key", "value", 0)
			Expect(c.Len()).To(Equal(1))

			c.Flush()
			Eventually(func() int {
				return c.Len()
			}).Should(BeZero())

			c.Close()
			Expect(c.Len()).To(BeZero())
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
			WithEvictionCallback(func(_, value interface{}) {
				defer GinkgoRecover()
				evicted <- value.(uint64)
			}).
			Build()
	})

	Specify("waits for closers to be closed", func() {
		value := uint64(0)

		// Fetch a value and keep it alive by not closing the closer.
		_, closer, _ := c.Fetch("key", time.Nanosecond, func() (interface{}, error) {
			return atomic.AddUint64(&value, 1), nil
		})

		// Let first value expire and fetch a new value.
		Eventually(func() uint64 {
			value, closer, _ := c.Fetch("key", time.Nanosecond, func() (interface{}, error) {
				return atomic.AddUint64(&value, 1), nil
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

		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("record is evicted concurrently with fetch callback", func() {
		Specify("eviction callback waits for fetch to finish", func() {
			value, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
				Expect(c.Exists("key")).To(BeFalse())
				// Evict will block until fetch callback returns.
				go c.Evict("key")
				// Key can now be evicted but shouldn't until we return.
				select {
				case <-evicted:
					Fail("did not expect evicted")
				case <-time.After(4 * evcache.SyncInterval):
				}

				return uint64(0), nil
			})
			Expect(closer).NotTo(BeNil())
			closer.Close()
			Expect(value).To(Equal(uint64(0)))

			// Eviction callback waited until Fetch returned a new value.
			Expect(<-evicted).To(Equal(uint64(0)))

			c.Close()
			Expect(c.Len()).To(BeZero())
		})
	})
})

var _ = Describe("Fetch fails with an error", func() {
	var (
		n        = 100
		errFetch error
		wg       sync.WaitGroup
		c        *evcache.Cache
	)

	BeforeEach(func() {
		errFetch = errors.New("error creating value")
		wg = sync.WaitGroup{}
		c = evcache.New().Build()
	})

	Specify("no records are cached", func() {
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

		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("record is evicted concurrently with fetch callback", func() {
		Specify("cache stays consistent", func() {
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					value, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
						return nil, errFetch
					})
					if err != nil {
						Expect(closer).To(BeNil())
						Expect(value).To(BeNil())
					} else {
						closer.Close()
						Expect(value).To(Equal("second value"))
					}
				}()

				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					c.Evict("key")
					value, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
						return "second value", nil
					})
					Expect(err).To(BeNil())
					closer.Close()
					Expect(value).To(Equal("second value"))
					c.Evict("key")
				}()
			}

			wg.Wait()

			c.Close()
			Expect(c.Len()).To(BeZero())
		})
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

	Specify("callback does not block record", func() {
		c.Set("key", "value", 0)
		Expect(c.Len()).To(Equal(1))

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			c.Range(func(key, value interface{}) bool {
				Expect(key).To(Equal("key"))
				Expect(value).To(Equal("value"))
				Expect(c.Exists(key)).To(BeTrue())

				_, closer, exists := c.Get(key)
				Expect(exists).To(BeTrue())
				closer.Close()

				c.Evict("key")
				Expect(<-evicted).To(Equal(key))
				Expect(c.Len()).To(BeZero())
				return true
			})
		}()

		wg.Wait()
		c.Close()
		Expect(c.Len()).To(BeZero())
	})
})

var _ = Describe("ordered iteration of records", func() {
	var (
		n = 10
		c *evcache.Cache
	)

	BeforeEach(func() {
		c = evcache.New().
			WithCapacity(uint32(n)).
			Build()
		for i := 0; i < n; i++ {
			c.Set(i, i, 0)
		}
		Expect(c.Len()).To(Equal(n))
	})

	Specify("Do is ordered", func() {
		var keys []int
		c.Do(func(key, value interface{}) bool {
			Expect(value).To(Equal(key))
			keys = append(keys, key.(int))
			return true
		})
		Expect(keys).To(HaveLen(n))
		Expect(sort.IntsAreSorted(keys)).To(BeTrue())

		c.Close()
		Expect(c.Len()).To(BeZero())
	})
})

var _ = Describe("overflow when setting values", func() {
	var (
		n        = 100
		overflow = 100
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
			for i := 0; i < n+overflow; i++ {
				c.Set(i, 0, 0)
				Expect(c.Len()).To(BeNumerically("<=", n), "capacity cannot be exceeded")
			}

			Eventually(func() uint64 {
				return atomic.LoadUint64(&evicted)
			}).Should(Equal(uint64(overflow)))

			c.Close()
			Expect(c.Len()).To(BeZero())
		})
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

			c.Close()
			Expect(c.Len()).To(BeZero())
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

			c.Close()
			Expect(c.Len()).To(BeZero())
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
		Entry(
			"Set",
			func(i int) {
				c.Set(i, "value", 0)
			},
		),
	)
})

var _ = Describe("overflow eviction order", func() {
	var (
		n           = 10
		key         uint64
		evictedKeys chan uint64
		c           *evcache.Cache
		warmupSet   = func() {
			for i := 1; i <= n; i++ {
				k := atomic.AddUint64(&key, 1)
				c.Set(k, "value", 0)
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
		warmupFetch = func() {
			for i := 1; i <= n; i++ {
				k := atomic.AddUint64(&key, 1)
				_, closer, _ := c.Fetch(k, 0, func() (interface{}, error) {
					return "value", nil
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
		overflow = func() (keys []int) {
			for i := 0; i < n; i++ {
				// Overflow the cache and catch the evicted keys.
				k := atomic.AddUint64(&key, 1)
				c.Set(k, nil, 0)
				evictedKey := int(<-evictedKeys)
				keys = append(keys, evictedKey)
			}
			return keys
		}
		pop = func() (keys []int) {
			for i := 0; i < n; i++ {
				key, value := c.Pop()
				evictedKey := <-evictedKeys
				Expect(evictedKey).To(Equal(key))
				Expect(value).To(Equal("value"))
				keys = append(keys, int(evictedKey))
			}
			return keys
		}
	)

	BeforeEach(func() {
		evictedKeys = make(chan uint64, n)
		key = 0
	})

	Context("LFU disabled", func() {
		BeforeEach(func() {
			c = evcache.New().
				WithEvictionCallback(func(key, _ interface{}) {
					defer GinkgoRecover()
					evictedKeys <- key.(uint64)
				}).
				WithCapacity(uint32(n)).
				Build()
		})

		Specify("the eviction order is sorted by eldest keys first", func() {
			warmupSet()

			keys := overflow()
			Expect(sort.IntsAreSorted(keys)).To(BeTrue())

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("pop order is sorted by eldest keys first", func() {
			warmupSet()

			keys := pop()
			Expect(sort.IntsAreSorted(keys)).To(BeTrue())

			c.Close()
			Expect(c.Len()).To(BeZero())
		})
	})

	Context("LFU enabled", func() {
		BeforeEach(func() {
			c = evcache.New().
				WithEvictionCallback(func(key, _ interface{}) {
					defer GinkgoRecover()
					evictedKeys <- key.(uint64)
				}).
				WithCapacity(uint32(n)).
				WithLFU().
				Build()
		})

		When("Set is used", func() {
			Specify("the eviction order is sorted by LFU keys first", func() {
				warmupSet()

				time.Sleep(2 * evcache.SyncInterval)

				keys := overflow()
				Expect(sort.IsSorted(sort.Reverse(sort.IntSlice(keys)))).To(BeTrue())

				c.Close()
				Expect(c.Len()).To(BeZero())
			})
		})

		When("Fetch is used", func() {
			Specify("the eviction order is sorted by LFU keys first", func() {
				warmupFetch()

				time.Sleep(2 * evcache.SyncInterval)

				keys := overflow()
				Expect(sort.IsSorted(sort.Reverse(sort.IntSlice(keys)))).To(BeTrue())

				c.Close()
				Expect(c.Len()).To(BeZero())
			})
		})

		Specify("pop order is sorted by LFU keys first", func() {
			warmupSet()

			time.Sleep(2 * evcache.SyncInterval)

			keys := pop()
			Expect(sort.IsSorted(sort.Reverse(sort.IntSlice(keys)))).To(BeTrue())

			c.Close()
			Expect(c.Len()).To(BeZero())
		})
	})
})
