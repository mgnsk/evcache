package evcache

import (
	"container/ring"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// SyncInterval is the interval for background loop.
//
// If cache overflows and records are created faster than
// SyncInterval, the LFU eviction starts losing order.
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

// Cache is an eventually consistent LFU cache.
//
// All methods except Close are safe to use concurrently.
type Cache struct {
	records    sync.Map
	pool       sync.Pool
	wg         sync.WaitGroup
	ring       *lfuRing
	afterEvict EvictionCallback
	quit       chan struct{}
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
	return func(c *Cache) {
		c.pool = sync.Pool{
			New: func() interface{} {
				return &record{ring: ring.New(1)}
			},
		}
		c.ring = newLFURing(0)
		c.quit = make(chan struct{})
	}
}

// WithEvictionCallback specifies an asynchronous eviction callback.
func (build Builder) WithEvictionCallback(cb EvictionCallback) Builder {
	return func(c *Cache) {
		build(c)

		c.afterEvict = cb
	}
}

// WithCapacity configures the cache with specified capacity.
// If cache exceeds the limit, the least frequently used
// record is evicted.
//
// New records are inserted as the most frequently used to
// reduce premature eviction of new but unused records.
func (build Builder) WithCapacity(capacity uint32) Builder {
	return func(c *Cache) {
		build(c)

		c.ring = newLFURing(capacity)
	}
}

// Build the cache.
func (build Builder) Build() *Cache {
	c := &Cache{}
	build(c)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.runLoop()
	}()
	return c
}

// Get returns the value stored in the cache for a key. The boolean indicates
// whether a value was found.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
func (c *Cache) Get(key interface{}) (value interface{}, closer io.Closer, exists bool) {
	for {
		old, ok := c.records.Load(key)
		if !ok {
			return nil, nil, false
		}
		oldrec := old.(*record)
		value, exists = oldrec.Load()
		if exists {
			return value, oldrec, true
		}
	}
}

// Fetch attempts to get or set the value and calls f on a miss to receive the new value.
// If f returns an error, no value is cached and the error is returned back to caller.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
func (c *Cache) Fetch(key interface{}, ttl time.Duration, f FetchCallback) (value interface{}, closer io.Closer, err error) {
	loadOrStore := func(r *record) (old *record, loaded bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if old, loaded := c.records.LoadOrStore(key, r); loaded {
			return old.(*record), true
		}
		value, err = f()
		if err != nil {
			c.Evict(key)
			return nil, false
		}
		r.init(value, ttl)
		if lfu := c.ring.Push(key, r.ring); lfu != nil {
			c.Evict(lfu)
		}
		return nil, false
	}
	r := c.pool.Get().(*record)
	for {
		old, loaded := loadOrStore(r)
		if err != nil {
			c.pool.Put(r)
			return nil, nil, err
		}
		if !loaded {
			return value, r, nil
		}
		if v, loaded := old.Load(); loaded {
			c.pool.Put(r)
			return v, old, nil
		}
	}
}

// Evict a key.
func (c *Cache) Evict(key interface{}) {
	value, loaded := c.records.LoadAndDelete(key)
	if !loaded {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		r := value.(*record)
		value, ok := r.LoadAndDelete()
		if !ok {
			return
		}
		if k := c.ring.Remove(r.ring); k != nil && k != key {
			panic("evcache: invalid ring")
		}
		r.wg.Wait()
		c.pool.Put(r)
		if c.afterEvict != nil {
			c.afterEvict(key, value)
		}
	}()
}

// Range calls f sequentially for each key and value present in the cache.
// If f returns false, range stops the iteration.
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

// Flush evicts all keys from the cache.
func (c *Cache) Flush() {
	c.records.Range(func(key, _ interface{}) bool {
		c.Evict(key)
		return true
	})
}

// Len returns the number of keys in the cache.
func (c *Cache) Len() int {
	return c.ring.Len()
}

// Close shuts down the cache, evicts all keys
// and waits for eviction callbacks to finish.
//
// It is not safe to close the cache
// while in use.
func (c *Cache) Close() error {
	close(c.quit)
	c.wg.Wait()
	c.Flush()
	c.wg.Wait()
	return nil
}

func (c *Cache) runLoop() {
	ticker := time.NewTicker(SyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.quit:
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
			c.Evict(key)
		} else if hits := atomic.SwapUint32(&r.hits, 0); hits > 0 {
			c.ring.Promote(r.ring, hits)
		}
		return true
	})
}
