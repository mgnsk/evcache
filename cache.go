package evcache

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// SyncInterval is the interval for background loop
// for autoexpiry and optional eventual LFU ordering.
//
// If cache overflows while LFU is enabled and records are created faster than
// SyncInterval can update record ordering, the eviction starts losing LFU order
// and will become the insertion order with eldest first.
var SyncInterval = time.Second

// FetchCallback is called when *Cache.Fetch has a miss
// and must return a new value or an error.
//
// It blocks the key from being accessed until the
// callback returns.
type FetchCallback func() (interface{}, error)

// EvictionCallback is an asynchronous callback which
// is called after cache key eviction.
//
// It waits until all io.Closers returned by Get or Fetch
// are closed.
type EvictionCallback func(key, value interface{})

// Cache is an in-memory ordered cache with optional eventually consistent LFU ordering.
type Cache struct {
	records    sync.Map
	pool       sync.Pool
	wg         sync.WaitGroup
	mu         sync.RWMutex
	onceLoop   sync.Once
	lfuEnabled bool
	list       *ringList
	afterEvict EvictionCallback
	stopLoop   chan struct{}
}

// Builder builds a cache.
type Builder func(*Cache)

// New creates an empty cache.
//
// Cache must be closed after usage has stopped
// to prevent a leaking resources.
//
// It is not safe to close the cache while in use.
func New() Builder {
	return func(c *Cache) {}
}

// WithEvictionCallback specifies an asynchronous eviction callback.
func (build Builder) WithEvictionCallback(cb EvictionCallback) Builder {
	return func(c *Cache) {
		build(c)
		c.afterEvict = cb
	}
}

// WithCapacity configures the cache with specified capacity.
//
// If cache exceeds the limit, the eldest record is evicted or
// if LFU is enabled, the least frequently used record is evicted.
func (build Builder) WithCapacity(capacity uint32) Builder {
	return func(c *Cache) {
		build(c)
		c.list = newRingList(capacity)
	}
}

// WithLFU enables eventual LFU ordering of records.
func (build Builder) WithLFU() Builder {
	return func(c *Cache) {
		build(c)
		c.lfuEnabled = true
	}
}

// Build the cache.
func (build Builder) Build() *Cache {
	c := &Cache{
		pool: sync.Pool{
			New: func() interface{} {
				return newRecord()
			},
		},
		stopLoop: make(chan struct{}),
	}
	build(c)
	if c.list == nil {
		c.list = newRingList(0)
	}
	if c.lfuEnabled {
		c.runLoop()
	}
	return c
}

// Exists returns whether a value in the cache exists for key.
func (c *Cache) Exists(key interface{}) bool {
	if r, ok := c.records.Load(key); ok && r.(*record).IsActive() {
		return true
	}
	return false
}

// Get returns the value stored in the cache for a key. The boolean indicates
// whether a value was found.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
func (c *Cache) Get(key interface{}) (value interface{}, closer io.Closer, exists bool) {
	for {
		r, ok := c.records.Load(key)
		if !ok {
			return nil, nil, false
		}
		value, exists = r.(*record).LoadAndHit()
		if exists {
			return value, r.(io.Closer), true
		}
	}
}

// Set the value in the cache for a key.
//
// If the cache has a capacity limit and it is exceeded,
// the least frequently used record or the eldest is evicted
// depending on whether LFU ordering is enabled or not.
func (c *Cache) Set(key, value interface{}, ttl time.Duration) {
	compareAndEvict := func(r *record) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.deleteIfEqualsLocked(key, r) {
			c.finalize(key, r)
		}
	}
	r := c.pool.Get().(*record)
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		old, loaded := c.records.LoadOrStore(key, r)
		if !loaded {
			if ttl > 0 {
				c.runLoop()
			}
			r.init(value, ttl)
			if front := c.list.PushBack(key, r.ring); front != nil {
				c.Evict(front)
			}
			return
		}
		compareAndEvict(old.(*record))
	}
}

// Fetch attempts to get or set the value and calls f on a miss to receive the new value.
// If f returns an error, no value is cached and the error is returned back to caller.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
//
// If the cache has a capacity limit and it is exceeded,
// the least frequently used record or the eldest is evicted
// depending on whether LFU ordering is enabled or not.
func (c *Cache) Fetch(key interface{}, ttl time.Duration, f FetchCallback) (value interface{}, closer io.Closer, err error) {
	r := c.pool.Get().(*record)
	loadOrStore := func() (old *record, loaded bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if old, loaded := c.records.LoadOrStore(key, r); loaded {
			return old.(*record), true
		}
		value, err = f()
		if err != nil {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.deleteIfEqualsLocked(key, r)
			return nil, false
		}
		if ttl > 0 {
			c.runLoop()
		}
		r.init(value, ttl)
		r.wg.Add(1)
		if front := c.list.PushBack(key, r.ring); front != nil {
			c.Evict(front)
		}
		return nil, false
	}
	for {
		old, loaded := loadOrStore()
		if err != nil {
			c.pool.Put(r)
			return nil, nil, err
		}
		if !loaded {
			return value, r, nil
		}
		if v, ok := old.LoadAndHit(); ok {
			c.pool.Put(r)
			return v, old, nil
		}
	}
}

// Range calls f for each key and value present in the cache in no particular order.
// If f returns false, Range stops the iteration.
//
// Range does not necessarily correspond to any consistent snapshot of the cache's
// contents: no key will be visited more than once, but if the value for any key
// is stored or deleted concurrently, Range may reflect any mapping for that key
// from any point during the Range call.
func (c *Cache) Range(f func(key, value interface{}) bool) {
	c.records.Range(func(key, value interface{}) bool {
		r := value.(*record)
		if !r.IsActive() {
			// Skip if Fetch callback is running
			// or deleted.
			return true
		}
		v, ok := r.Load()
		if !ok {
			return true
		}
		return f(key, v)
	})
}

// Do calls f sequentially for each key and value present in the cache
// in order. If f returns false, Do stops the iteration.
// When LFU is used, the order is from least to most frequently used,
// otherwise it is the insertion order with eldest first by default.
//
// It is not safe to use Do concurrently with any other method
// except Exists, Get or Fetch if the value is known to exist.
// f is not allowed to modify the cache.
func (c *Cache) Do(f func(key, value interface{}) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list.Do(func(key interface{}) bool {
		r, ok := c.records.Load(key)
		if !ok {
			panic("evcache: Do used concurrently")
		}
		return f(key, r.(*record).value)
	})
}

// Pop evicts and returns the oldest key and value. If LFU ordering is
// enabled, then the least frequently used key and value.
func (c *Cache) Pop() (key, value interface{}) {
	for {
		if key = c.list.Pop(); key == nil {
			return nil, nil
		}
		if value, ok := c.Evict(key); ok {
			return key, value
		}
	}
}

// Evict evicts a key and returns its value.
func (c *Cache) Evict(key interface{}) (interface{}, bool) {
	c.mu.RLock()
	r, ok := c.records.LoadAndDelete(key)
	if !ok {
		c.mu.RUnlock()
		return nil, false
	}
	c.mu.RUnlock()
	return c.finalize(key, r.(*record))
}

// Flush evicts all keys from the cache.
func (c *Cache) Flush() {
	c.records.Range(func(key, _ interface{}) bool {
		c.Evict(key)
		return true
	})
}

// Len returns the number of keys in the cache.
func (c *Cache) Len() int {
	return c.list.Len()
}

// Close shuts down the cache, evicts all keys
// and waits for eviction callbacks to finish.
//
// It is not safe to close the cache
// while in use.
func (c *Cache) Close() error {
	c.runLoop()
	c.stopLoop <- struct{}{}
	close(c.stopLoop)
	c.Flush()
	c.wg.Wait()
	return nil
}

func (c *Cache) deleteIfEqualsLocked(key interface{}, r *record) bool {
	old, ok := c.records.Load(key)
	if !ok {
		return false
	}
	if old.(*record) != r {
		return false
	}
	old, ok = c.records.LoadAndDelete(key)
	if !ok || old.(*record) != r {
		panic("evcache: inconsistent map state")
	}
	return true
}

func (c *Cache) finalize(key interface{}, r *record) (interface{}, bool) {
	value, ok := r.LoadAndReset()
	if !ok {
		// An inactive record which had a concurrent Fetch and failed.
		return nil, false
	}
	if k := c.list.Remove(r.ring); k != nil && k != key {
		panic("evcache: invalid ring")
	}
	if c.afterEvict != nil {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			r.wg.Wait()
			c.pool.Put(r)
			if c.afterEvict != nil {
				c.afterEvict(key, value)
			}
		}()
	} else {
		c.pool.Put(r)
	}
	return value, true
}

func (c *Cache) runLoop() {
	c.onceLoop.Do(func() {
		go func() {
			ticker := time.NewTicker(SyncInterval)
			defer ticker.Stop()
			for {
				select {
				case <-c.stopLoop:
					return
				case now := <-ticker.C:
					c.processRecords(now.UnixNano())
				}
			}
		}()
	})
}

func (c *Cache) processRecords(now int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records.Range(func(key, value interface{}) bool {
		r := value.(*record)
		if !r.IsActive() {
			return true
		}
		if r.expired(now) {
			if c.deleteIfEqualsLocked(key, r) {
				c.finalize(key, r)
			}
			return true
		}
		if c.lfuEnabled {
			if hits := atomic.SwapUint32(&r.hits, 0); hits > 0 {
				c.list.MoveForward(r.ring, hits)
			}
		}
		return true
	})
}
