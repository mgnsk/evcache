package evcache

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// SyncInterval is the interval for background loop
// for autoexpiry and optional eventual LFU eviction ordering.
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

// Cache is an eventually consistent ordered cache.
//
// By default, records are sorted by insertion order.
//
// All methods except Close and OrderedRange are safe to use concurrently.
type Cache struct {
	records    sync.Map
	pool       sync.Pool
	wg         sync.WaitGroup
	mu         sync.RWMutex
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
// to prevent a leaking goroutine.
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
// If cache exceeds the limit, the eldest record is evicted.
func (build Builder) WithCapacity(capacity uint32) Builder {
	return func(c *Cache) {
		build(c)
		c.list = newRingList(capacity)
	}
}

// WithLFU enables the near-LFU eviction order of records.
//
// New records are inserted as the most frequently used to
// reduce premature eviction of new but unused records.
func (build Builder) WithLFU() Builder {
	return func(c *Cache) {
		build(c)
		if c.list == nil {
			panic("evcache: WithLFU requires WithCapacity")
		}
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
	go c.runLoop()
	return c
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

// Set a value in the cache for a key.
func (c *Cache) Set(key, value interface{}, ttl time.Duration) {
	r := c.pool.Get().(*record)
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		old, loaded := c.records.LoadOrStore(key, r)
		if !loaded {
			r.init(value, ttl)
			if front := c.list.PushBack(key, r.ring); front != nil {
				c.Evict(front)
			}
			return
		}
		func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.deleteIfEqualsLocked(key, old.(*record)) {
				c.finalizeAsync(key, old.(*record))
			}
		}()
	}
}

// Fetch attempts to get or set the value and calls f on a miss to receive the new value.
// If f returns an error, no value is cached and the error is returned back to caller.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
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
		r.initAndHit(value, ttl)
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
		if v, loaded := old.LoadAndHit(); loaded {
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
		r.mu.RLock()
		if !r.IsActive() {
			r.mu.RUnlock()
			return true
		}
		v := r.value
		r.mu.RUnlock()
		return f(key, v)
	})
}

// OrderedRange calls f sequentially for each key and value present in the cache
// in order. If f returns false, Range stops the iteration.
// When LFU is used, the order is from least to most frequently used,
// otherwise it is the insertion order with eldest first by default.
//
// It is not safe to use OrderedRange concurrently with any other method.
func (c *Cache) OrderedRange(f func(key, value interface{}) bool) {
	c.stopLoop <- struct{}{}
	c.wg.Wait()
	defer func() {
		go c.runLoop()
	}()
	c.list.Do(func(key interface{}) bool {
		r, ok := c.records.Load(key)
		if !ok {
			return true
		}
		value, ok := r.(*record).Load()
		if !ok {
			return true
		}
		return f(key, value)
	})
}

// Evict a key.
func (c *Cache) Evict(key interface{}) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, loaded := c.records.LoadAndDelete(key)
	if !loaded {
		return
	}
	c.finalizeAsync(key, r.(*record))
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
	c.stopLoop <- struct{}{}
	close(c.stopLoop)
	c.Flush()
	c.wg.Wait()
	return nil
}

func (c *Cache) deleteIfEqualsLocked(key interface{}, r *record) bool {
	old, loaded := c.records.Load(key)
	if !loaded {
		return false
	}
	if old.(*record) != r {
		return false
	}
	old, loaded = c.records.LoadAndDelete(key)
	if loaded && old.(*record) != r {
		panic("evcache: inconsistent map state")
	}
	return true
}

func (c *Cache) finalizeAsync(key interface{}, r *record) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		value, loaded := r.LoadAndReset()
		if !loaded {
			// An inactive record which had a concurrent Fetch and failed.
			return
		}
		if k := c.list.Remove(r.ring); k != nil && k != key {
			panic("evcache: invalid ring")
		}
		r.wg.Wait()
		c.pool.Put(r)
		if c.afterEvict != nil {
			c.afterEvict(key, value)
		}
	}()
}

func (c *Cache) runLoop() {
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
}

func (c *Cache) processRecords(now int64) {
	c.records.Range(func(key, value interface{}) bool {
		r := value.(*record)
		if !r.IsActive() {
			return true
		}
		r.mu.RLock()
		defer r.mu.RUnlock()
		if !r.IsActive() {
			return true
		}
		if r.expires > 0 && r.expires < now {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.deleteIfEqualsLocked(key, r) {
				c.finalizeAsync(key, r)
			}
			return true
		}
		if !c.lfuEnabled {
			return true
		}
		if hits := atomic.SwapUint32(&r.hits, 0); hits > 0 {
			c.list.MoveForward(r.ring, hits)
		}
		return true
	})
}
