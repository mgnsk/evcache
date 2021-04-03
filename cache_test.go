package evcache_test

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mgnsk/evcache/v2"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
)

var _ = BeforeSuite(func() {
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

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("key exists", func() {
		BeforeEach(func() {
			c.Set("key", 0, 0)
			Expect(c.Len()).To(Equal(1))
			Expect(c.Exists("key")).To(BeTrue())
		})

		Specify("value is replaced", func() {
			c.Set("key", 1, 0)
			Expect(c.Len()).To(Equal(1))
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
})

var _ = Describe("Fetch callback", func() {
	var (
		evicted chan interface{}
		wg      sync.WaitGroup
		c       *evcache.Cache
	)

	BeforeEach(func() {
		evicted = make(chan interface{}, 2)
		wg = sync.WaitGroup{}
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

	AfterEach(func() {
		wait(&wg)
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("callback blocks and same key is accessed", func() {
		var (
			done    uint64
			valueCh chan interface{}
		)
		BeforeEach(func() {
			done = 0
			valueCh = make(chan interface{})

			fetchStarted := make(chan struct{})
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
					close(fetchStarted)
					v := <-valueCh
					if !atomic.CompareAndSwapUint64(&done, 0, 1) {
						Fail("expected for fetch callback to return first")
					}
					if err, ok := v.(error); ok {
						return nil, err
					}
					return v, nil
				})
				if closer != nil {
					closer.Close()
				}
			}()
			<-fetchStarted
		})

		Specify("Exists will not block and returns false", func() {
			defer close(valueCh)
			Expect(c.Exists("key")).To(BeFalse())
		})

		Specify("Evict will not block and returns false", func() {
			defer close(valueCh)
			_, ok := c.Evict("key")
			Expect(ok).To(BeFalse())
		})

		Specify("Get will not block and returns false", func() {
			defer close(valueCh)
			_, _, ok := c.Get("key")
			Expect(ok).To(BeFalse())
		})

		Specify("Pop will not block and returns nil", func() {
			defer close(valueCh)
			key, _ := c.Pop()
			Expect(key).To(BeNil())
		})

		Context("Fetch callback does not return an error", func() {
			BeforeEach(func() {
				time.AfterFunc(4*evcache.SyncInterval, func() {
					valueCh <- "value"
				})
			})

			Specify("Fetch blocks and returns old value", func() {
				v, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
					panic("unexpected fetch callback")
				})
				if atomic.CompareAndSwapUint64(&done, 0, 1) {
					Fail("expected first Fetch to have returned first")
				}
				Expect(err).To(BeNil())
				closer.Close()
				Expect(v).To(Equal("value"))
			})

			Specify("Set blocks and overwrites value", func() {
				c.Set("key", "value1", 0)
				if atomic.CompareAndSwapUint64(&done, 0, 1) {
					Fail("expected first Fetch to have returned first")
				}
				value, closer, ok := c.Get("key")
				Expect(ok).To(BeTrue())
				closer.Close()
				Expect(value).To(Equal("value1"))
			})
		})

		Context("Fetch callback returns an error", func() {
			BeforeEach(func() {
				time.AfterFunc(4*evcache.SyncInterval, func() {
					valueCh <- errors.New("error fetching")
				})
			})

			Specify("Fetch blocks until first Fetch fails and then fetches a new value", func() {
				v, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
					return "new value", nil
				})
				if atomic.CompareAndSwapUint64(&done, 0, 1) {
					Fail("expected first Fetch to have returned first")
				}
				Expect(err).To(BeNil())
				closer.Close()
				Expect(v).To(Equal("new value"))
			})

			Specify("Set blocks and sets the value", func() {
				c.Set("key", "value1", 0)
				if atomic.CompareAndSwapUint64(&done, 0, 1) {
					Fail("expected first Fetch to have returned first")
				}
				value, closer, ok := c.Get("key")
				Expect(ok).To(BeTrue())
				closer.Close()
				Expect(value).To(Equal("value1"))
			})
		})

		Specify("autoexpiry keeps working", func() {
			// Verify other keys can expire.
			c.Set("key1", "value1", 4*evcache.SyncInterval)

			len1 := c.Len()
			k := <-evicted
			len2 := c.Len()

			// Finally send a value and unblock the first key.
			valueCh <- "value"

			Expect(len1).To(Equal(1))
			Expect(k).To(Equal("key1"))
			Expect(len2).To(BeZero())

			Eventually(func() bool {
				v, closer, exists := c.Get("key")
				if !exists {
					return false
				}
				if atomic.CompareAndSwapUint64(&done, 0, 1) {
					Fail("expected first Fetch to have returned first")
				}
				closer.Close()
				Expect(v).To(Equal("value"))
				return true
			}).Should(BeTrue())

			Expect(c.Len()).To(Equal(1))
		})

		Specify("Range skips the blocking key", func() {
			defer func() {
				valueCh <- "value"
			}()

			c.Set("key1", "value1", 0)
			Expect(c.Len()).To(Equal(1))

			n := 0
			c.Range(func(key, value interface{}) bool {
				if key == "key" {
					Fail("expected to skip key")
				}
				v, ok := c.Evict("key1")
				Expect(ok).To(BeTrue())
				Expect(v).To(Equal("value1"))
				n++
				return true
			})
			Expect(n).To(Equal(1))
		})

		Specify("Do skips the blocking key", func() {
			defer func() {
				valueCh <- "value"
			}()

			c.Set("key1", "value1", 0)
			Expect(c.Len()).To(Equal(1))

			n := 0
			// Do must not modify the cache.
			c.Do(func(key, value interface{}) bool {
				if key == "key" {
					Fail("expected to skip key")
				}
				n++
				return true
			})
			Expect(n).To(Equal(1))
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
		BeforeEach(func() {
			c.Set("key", "value", 0)
			Expect(c.Len()).To(Equal(1))
		})

		Specify("it is evicted", func() {
			v, ok := c.Evict("key")
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal("value"))
			Expect(c.Exists("key")).To(BeFalse())
			Expect(c.Len()).To(BeZero())
		})
	})

	When("record is active", func() {
		BeforeEach(func() {
			c.Set("key", "value", 0)
			Expect(c.Len()).To(Equal(1))
		})

		Specify("it is evicted", func() {
			_, closer, _ := c.Get("key")
			defer closer.Close()

			v, ok := c.Evict("key")
			Expect(ok).To(BeTrue())
			Expect(v).To(Equal("value"))
			Expect(c.Exists("key")).To(BeFalse())
			Expect(c.Len()).To(BeZero())
		})
	})
})

var _ = Describe("compare and evict", func() {
	var c *evcache.Cache

	BeforeEach(func() {
		c = evcache.New().Build()
		c.Set("key", "value", 0)
		Expect(c.Len()).To(Equal(1))
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	When("value is deeply equal", func() {
		Specify("it is evicted", func() {
			ok := c.CompareAndEvict("key", "value")
			Expect(ok).To(BeTrue())
			Expect(c.Exists("key")).To(BeFalse())
			Expect(c.Len()).To(BeZero())
		})
	})

	When("value is not deeply equal", func() {
		Specify("it is not evicted", func() {
			// If we load a value, use it and then encounter an error
			// we might want to evict the value.
			//
			// It we would call c.Evict("key") we might mistakenly evict
			// a new value if key had concurrently changed after loading
			// the value. Only evict the old value if not yet evicted.
			ok := c.CompareAndEvict("key", "old value")
			Expect(ok).To(BeFalse())
			Expect(c.Exists("key")).To(BeTrue())
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
			c.Set("key", "value", 0)
			Expect(c.Len()).To(Equal(1))

			c.Flush()
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

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
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
	})

	When("record is evicted concurrently with fetch callback", func() {
		Specify("eviction callback waits for fetch to finish", func() {
			wg := sync.WaitGroup{}
			value, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
				Expect(c.Exists("key")).To(BeFalse())
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					Eventually(func() bool {
						// Evict won't evict before fetch callback returns.
						v, ok := c.Evict("key")
						if ok {
							Expect(v).To(Equal(uint64(0)))
						}
						return ok
					}).Should(BeTrue())
				}()
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

			wait(&wg)
		})
	})
})

var _ = Describe("eviction callback with ModeNonBlocking", func() {
	var (
		evicted chan string
		c       *evcache.Cache
	)

	BeforeEach(func() {
		evicted = make(chan string)
		c = evcache.New().
			WithEvictionCallback(func(_, value interface{}) {
				defer GinkgoRecover()
				evicted <- value.(string)
			}).
			WithEvictionMode(evcache.ModeNonBlocking).
			Build()
	})

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("callback does not block key", func() {
		c.Set("key", "value1", 0)
		value1, _ := c.Evict("key")

		c.Set("key", "value2", 0)
		value2, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
			return "value1", nil
		})
		defer closer.Close()

		<-evicted
		go func() {
			<-evicted
		}()

		Expect(value1).To(Equal("value1"))
		Expect(value2).To(Equal("value2"))
	})
})

var _ = Describe("eviction callback with ModeBlocking", func() {
	var (
		evicted         chan string
		callbackStarted chan struct{}
		once            sync.Once
		c               *evcache.Cache
	)

	BeforeEach(func() {
		evicted = make(chan string)
		callbackStarted = make(chan struct{})
		once = sync.Once{}
		c = evcache.New().
			WithEvictionCallback(func(_, value interface{}) {
				once.Do(func() {
					close(callbackStarted)
				})
				defer GinkgoRecover()
				evicted <- value.(string)
			}).
			WithEvictionMode(evcache.ModeBlocking).
			Build()
	})

	When("callback is running", func() {
		BeforeEach(func() {
			c.Set("key", "value", 0)
			c.Evict("key")
			<-callbackStarted
		})

		Specify("Exists will not block and returns false", func() {
			ok := c.Exists("key")
			<-evicted
			Expect(ok).To(BeFalse())

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("Evict will not block and returns false", func() {
			_, ok := c.Evict("key")
			<-evicted
			Expect(ok).To(BeFalse())

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("Get will not block and returns false", func() {
			_, _, ok := c.Get("key")
			<-evicted
			Expect(ok).To(BeFalse())

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("Pop will not block and returns nil", func() {
			key, _ := c.Pop()
			<-evicted
			Expect(key).To(BeNil())

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("Range will not block and skips the key", func() {
			c.Range(func(key, value interface{}) bool {
				panic("unexpected Range callback")
			})
			<-evicted

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		Specify("Do will not block and skips the key", func() {
			c.Do(func(key, value interface{}) bool {
				panic("unexpected Do callback")
			})
			<-evicted

			c.Close()
			Expect(c.Len()).To(BeZero())
		})

		DescribeTable(
			"blocking calls",
			func(f func()) {
				defer GinkgoRecover()
				ret := make(chan struct{})
				go func() {
					defer close(ret)
					defer GinkgoRecover()
					f()
				}()

				// This is safe only because evicted chan has no buffer.
				// ret couldn't have been closed until we receive from evicted.
				select {
				case <-evicted:
					<-ret
				case <-ret:
					Fail("expected eviction callback to return first")
					<-evicted
				}

				go func() {
					// Unblock c.Close.
					<-evicted
				}()

				c.Close()
				Expect(c.Len()).To(BeZero())
			},
			Entry(
				"Fetch blocks and returns a new value",
				func() {
					v, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
						return "value1", nil
					})
					defer closer.Close()
					Expect(v).To(Equal("value1"))
				},
			),
			Entry(
				"Set blocks and overwrites the value",
				func() {
					c.Set("key", "value1", 0)
					value, closer, _ := c.Get("key")
					defer closer.Close()
					Expect(value).To(Equal("value1"))
				},
			),
		)
	})
})

var _ = Describe("blocking in ModeBlocking", func() {
	var (
		once         sync.Once
		readerDone   uint64
		evictionDone uint64
		c            *evcache.Cache
	)

	BeforeEach(func() {
		once = sync.Once{}
		readerDone = 0
		evictionDone = 0
		c = evcache.New().
			WithEvictionCallback(func(_, _ interface{}) {
				defer GinkgoRecover()
				once.Do(func() {
					time.Sleep(40 * time.Millisecond)
					if !atomic.CompareAndSwapUint64(&evictionDone, 0, 1) {
						Fail("expected for eviction callback to finish before Fetch and Set")
					}
				})
			}).
			WithEvictionMode(evcache.ModeBlocking).
			Build()
	})

	Specify("new writers wait for old readers to finish for key", func() {
		v, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
			return "value", nil
		})

		evictedValue, ok := c.Evict("key")
		Expect(ok).To(BeTrue())
		Expect(evictedValue).To(Equal(v))

		Expect(c.Len()).To(BeZero())

		wg := sync.WaitGroup{}

		wg.Add(1)
		time.AfterFunc(20*time.Millisecond, func() {
			defer wg.Done()
			defer GinkgoRecover()
			if !atomic.CompareAndSwapUint64(&readerDone, 0, 1) {
				Fail("expected for close to return first")
			}
			closer.Close()
		})

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			_, closer, _ := c.Fetch("key", 0, func() (interface{}, error) {
				return "value1", nil
			})
			if atomic.CompareAndSwapUint64(&readerDone, 0, 1) {
				Fail("expected to wait until old value closed")
			}
			if atomic.CompareAndSwapUint64(&evictionDone, 0, 1) {
				Fail("expected for eviction callback to finish before Fetch and Set")
			}
			closer.Close()
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			c.Set("key", "value2", 0)
			if atomic.CompareAndSwapUint64(&readerDone, 0, 1) {
				Fail("expected to wait until old value closed")
			}
			if atomic.CompareAndSwapUint64(&evictionDone, 0, 1) {
				Fail("expected for eviction callback to finish before Fetch and Set")
			}
		}()

		wg.Wait()

		c.Close()
		Expect(c.Len()).To(BeZero())
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

	AfterEach(func() {
		wait(&wg)
		c.Close()
		Expect(c.Len()).To(BeZero())
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
	})

	When("Set and Evict concurrently with fetch callback", func() {
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
						Expect(value).To(Equal("value"))
					}
				}()

				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					c.Set("key", "value", 0)
				}()

				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					c.Evict("key")
				}()

				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					c.Evict("key")
					value, closer, err := c.Fetch("key", 0, func() (interface{}, error) {
						return "value", nil
					})
					Expect(err).To(BeNil())
					closer.Close()
					Expect(value).To(Equal("value"))
				}()
			}
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

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	Specify("callback does not block record", func() {
		c.Set("key", "value", 0)
		Expect(c.Len()).To(Equal(1))

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

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
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
	})
})

var _ = Describe("overflowed record eviction", func() {
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

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
		Expect(evicted).To(Equal(uint64(n + overflow)))
	})

	When("Set causes an overflow", func() {
		Specify("eventually overflowed records are evicted", func() {
			for i := 0; i < n+overflow; i++ {
				c.Set(i, 0, 0)
				Expect(c.Len()).To(BeNumerically("<=", n), "capacity cannot be exceeded")
			}
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
		})
	})

	DescribeTable(
		"concurrent overflow",
		func(cb func(int)) {
			var wg sync.WaitGroup
			for i := 0; i < n+overflow; i++ {
				i := i
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					cb(i)
					Expect(c.Len()).To(BeNumerically("<=", n), "capacity cannot be exceeded")
				}()
			}

			wait(&wg)
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

var _ = Describe("concurrency test", func() {
	var (
		concurrency = 32
		n           = 100
		key         uint64
		wg          sync.WaitGroup
		c           *evcache.Cache
	)

	BeforeEach(func() {
		key = 0
		wg = sync.WaitGroup{}
		c = evcache.New().
			WithCapacity(10).
			Build()
	})

	AfterEach(func() {
		wait(&wg)
		c.Close()
		Expect(c.Len()).To(BeZero())
	})

	// Depends on Evict being called outside of record lock in c.Fetch.
	Specify("Fetch doesn't deadlock", func() {
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < n; i++ {
					_, _, _ = c.Fetch(atomic.AddUint64(&key, 1), 0, func() (interface{}, error) {
						return 0, nil
					})
				}
			}()
		}
	})

	// Depends on Evict being called outside of record lock in Set.
	Specify("Set doesn't deadlock", func() {
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < n; i++ {
					c.Set(atomic.AddUint64(&key, 1), 0, 0)
				}
			}()
		}
	})
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

	AfterEach(func() {
		c.Close()
		Expect(c.Len()).To(BeZero())
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
		})

		Specify("pop order is sorted by eldest keys first", func() {
			warmupSet()

			keys := pop()
			Expect(sort.IntsAreSorted(keys)).To(BeTrue())
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
			})
		})

		When("Fetch is used", func() {
			Specify("the eviction order is sorted by LFU keys first", func() {
				warmupFetch()

				time.Sleep(2 * evcache.SyncInterval)

				keys := overflow()
				Expect(sort.IsSorted(sort.Reverse(sort.IntSlice(keys)))).To(BeTrue())
			})
		})

		Specify("pop order is sorted by LFU keys first", func() {
			warmupSet()

			time.Sleep(2 * evcache.SyncInterval)

			keys := pop()
			Expect(sort.IsSorted(sort.Reverse(sort.IntSlice(keys)))).To(BeTrue())
		})
	})
})
