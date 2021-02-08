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
	list       *lfuRing
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
		c.list = newLFUList(0)
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
// If cache exceeds the limit, the least frequently
// will begin to be evicted.
//
// New records are inserted as the most frequently used to
// reduce premature eviction of new but unused records.
func (build Builder) WithCapacity(capacity uint32) Builder {
	return func(c *Cache) {
		build(c)

		c.list = newLFUList(capacity)
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

// Flush evicts all keys from the cache.
func (c *Cache) Flush() {
	c.records.Range(func(key, _ interface{}) bool {
		c.Delete(key)
		return true
	})
}

// Len returns the number of keys in the cache.
func (c *Cache) Len() int {
	return c.list.Len()
}

// Load returns the value stored in the cache for a key. The boolean indicates
// whether a value was found.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
func (c *Cache) Load(key interface{}) (value interface{}, closer io.Closer, exists bool) {
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
	new := c.pool.Get().(*record)
	for {
		new.mu.Lock()
		old, loaded := c.records.LoadOrStore(key, new)
		if !loaded {
			defer new.mu.Unlock()
			value, err := f()
			if err != nil {
				c.pool.Put(new)
				c.Delete(key)
				return nil, nil, err
			}
			new.init(value, ttl)
			lfu := c.list.Push(key, new.ring)
			if lfu != nil {
				c.Delete(lfu)
			}
			return value, new, nil
		}
		new.mu.Unlock()

		oldrec := old.(*record)
		value, exists := oldrec.Load()
		if exists {
			c.pool.Put(new)
			return value, oldrec, nil
		}
	}
}

// Delete a key.
func (c *Cache) Delete(key interface{}) {
	value, loaded := c.records.LoadAndDelete(key)
	if !loaded {
		return
	}
	c.wg.Add(1)
	go func() {
		r := value.(*record)
		defer c.wg.Done()
		value, ok := r.LoadAndDelete()
		if !ok {
			return
		}
		if c.list.Remove(r.ring) != key {
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
		r.mu.RLock()
		defer r.mu.RUnlock()
		if !r.alive {
			return true
		}
		if r.expires > 0 && r.expires < now {
			c.Delete(key)
		} else if hits := atomic.SwapUint32(&r.hits, 0); hits > 0 {
			c.list.Promote(r.ring, hits)
		}
		return true
	})
}
