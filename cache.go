package evcache

import (
	"container/list"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// EvictionInterval is the interval for background loop
// which handles key expiry.
var EvictionInterval = time.Second

// Factory is called when *Cache.Fetch has a miss.
//
// It blocks the same key from being accessed until
// the callbak returns and must return a new value
// or an error.
type Factory func() (interface{}, error)

// ExpiryCallback is a synchronous callback
// called when a cache key expires.
//
// It blocks the same key from being set until
// the callback returns. It does not block the key
// from being read.
//
// The returned boolean indicates whether to evict the key
// when returning true or extend the time to live by the
// original ttl value when returning false.
type ExpiryCallback func(key, value interface{}) (evict bool)

// EvictionCallback is an asynchronous callback
// called after cache key eviction.
//
// It is not called until all io.Closers returned
// alongside the value have been closed.
type EvictionCallback func(key, value interface{})

// Cache is a sync.Map wrapper with eventually consistent
// LFU eviction.
//
// All methods except Close are safe to use concurrently.
type Cache struct {
	records       sync.Map
	wg            sync.WaitGroup
	onceLoop      sync.Once
	pool          sync.Pool
	mu            sync.RWMutex
	cond          *sync.Cond
	listMu        sync.Mutex
	list          *list.List
	evictRequests chan struct{}
	quit          chan struct{}
	onExpiry      ExpiryCallback
	afterEvict    EvictionCallback
	capacity      uint32
	backpressure  uint32
	size          uint32
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
				return &record{}
			},
		}
		c.quit = make(chan struct{})
	}
}

// OnExpiry specifies a synchronous expiry callback.
func (build Builder) OnExpiry(cb ExpiryCallback) Builder {
	return func(c *Cache) {
		build(c)

		c.onExpiry = cb
	}
}

// AfterEviction specifies an asynchronous eviction callback.
func (build Builder) AfterEviction(cb EvictionCallback) Builder {
	return func(c *Cache) {
		build(c)

		c.afterEvict = cb
	}
}

// Capacity configures the cache with specified size limit.
// If cache exceeds the limit, the least frequently
// used records are evicted.
//
// New records are inserted as the most frequently used
// to reduce premature eviction of new but unused records.
//
// The maximum overflow at any given moment is the number of concurrent users.
// To limit overflow, limit concurrency.
func (build Builder) Capacity(size uint32) Builder {
	return func(c *Cache) {
		build(c)

		c.cond = sync.NewCond(&c.mu)
		c.list = list.New()
		c.evictRequests = make(chan struct{}, 1)
		c.capacity = size
	}
}

// Build the cache.
func (build Builder) Build() *Cache {
	c := &Cache{}
	build(c)

	return c
}

// Get returns the value stored in the cache for a key, or an error
// if no value is present.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
func (c *Cache) Get(key interface{}) (value interface{}, closer io.Closer, err error) {
	for {
		rec, ok := c.records.Load(key)
		if !ok {
			return nil, nil, ErrNotFound
		}
		r := rec.(*record)
		if value, ok := r.load(); ok {
			return value, r, nil
		}
	}
}

// Fetch attempts to get or set the value and calls f on a miss to receive the new value.
// If f returns an error, no value is cached and the error is returned back to caller.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
//
// If the cache has a size limit and it is exceeded, it may block
// until the overflow is evicted.
func (c *Cache) Fetch(key interface{}, ttl time.Duration, f Factory) (value interface{}, closer io.Closer, err error) {
	c.doGuarded(func() {
		var r *record
		for {
			r = c.pool.Get().(*record)
			r.mu.Lock()
			old, loaded := c.records.LoadOrStore(key, r)
			if !loaded {
				defer r.mu.Unlock()
				break
			}
			r.mu.Unlock()
			c.pool.Put(r)

			val, loaded := old.(*record).load()
			if loaded {
				value = val
				closer = old.(*record)
				return
			}
		}

		value, err = f()
		if err != nil {
			c.records.Delete(key)
			r.delete()
			return
		}
		closer = r

		r.wg.Add(1)
		r.hits = 1
		c.initRecord(r, value, ttl)
		atomic.AddUint32(&c.size, 1)
	})

	return value, closer, err
}

// Set the value for a key.
//
// If the cache has a size limit and it is exceeded, it may block
// until the overflow is evicted.
func (c *Cache) Set(key, value interface{}, ttl time.Duration) (replaced bool) {
	c.doGuarded(func() {
		var r *record
		for {
			r = c.pool.Get().(*record)
			r.mu.Lock()
			old, loaded := c.records.LoadOrStore(key, r)
			if !loaded {
				defer r.mu.Unlock()
				break
			}
			if c.swap(key, old.(*record), r) {
				replaced = true
				defer r.mu.Unlock()
				break
			}
			r.mu.Unlock()
			c.pool.Put(r)
		}

		c.initRecord(r, value, ttl)
		if !replaced {
			atomic.AddUint32(&c.size, 1)
		}
	})

	return replaced
}

// Evict a key and return its value if it exists.
func (c *Cache) Evict(key interface{}) (value interface{}, evicted bool) {
	rec, loaded := c.records.LoadAndDelete(key)
	if !loaded {
		return nil, false
	}

	r := rec.(*record)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isDeleted() {
		return nil, false
	}
	r.delete()
	c.runAfterEvict(key, r)
	atomic.AddUint32(&c.size, ^uint32(0))

	return r.value, true
}

// Flush evicts all keys from the cache.
func (c *Cache) Flush() {
	c.records.Range(func(key, _ interface{}) bool {
		c.Evict(key)
		return true
	})
}

// Len returns the number of keys in the cache.
func (c *Cache) Len() (length int) {
	return int(atomic.LoadUint32(&c.size))
}

// Close shuts down the cache, evicts all keys
// and waits for eviction callbacks to finish.
//
// It is not safe to close the cache
// while in use.
func (c *Cache) Close() error {
	c.onceLoop.Do(func() {
		go c.runEvictionLoop()
	})
	c.quit <- struct{}{}
	close(c.quit)
	c.wg.Wait()
	c.records.Range(func(key, _ interface{}) bool {
		c.Evict(key)
		return true
	})
	c.wg.Wait()

	return nil
}

func (c *Cache) runAfterEvict(key interface{}, r *record) {
	if r.elem != nil {
		c.listMu.Lock()
		c.list.Remove(r.elem)
		c.listMu.Unlock()
	}
	if c.afterEvict != nil {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			r.wg.Wait()
			c.afterEvict(key, r.value)
		}()
	}
}

func (c *Cache) swap(key interface{}, old, new *record) (swapped bool) {
	old.mu.Lock()
	defer old.mu.Unlock()
	if old.isDeleted() {
		return false
	}
	c.records.Store(key, new)
	old.delete()
	c.runAfterEvict(key, old)
	return true
}

func (c *Cache) initRecord(r *record, value interface{}, ttl time.Duration) {
	r.value = value
	if ttl > 0 {
		r.ttl = ttl
		r.expires = time.Now().Add(ttl).UnixNano()
		c.onceLoop.Do(func() {
			go c.runEvictionLoop()
		})
	}
}

func (c *Cache) wouldOverflow(size, bp uint32) bool {
	if size > c.capacity && size-c.capacity > bp {
		panic(fmt.Sprintf("evcache: invariant failed: overflow cannot exceed backpressure, overflow %d, bp: %d", size-c.capacity, bp))
	}
	return size+bp > c.capacity
}

func (c *Cache) triggerEviction() {
	select {
	case c.evictRequests <- struct{}{}:
	default:
		// Hitchhiked another request.
	}
}

func (c *Cache) doGuarded(f func()) {
	if c.capacity > 0 {
		// It is important for size to be loaded
		// before incrementing backpressure or a
		// race condition will occur and fail
		// the overflow <= backpressure invariant.
		size := atomic.LoadUint32(&c.size)
		bp := atomic.AddUint32(&c.backpressure, 1)
		defer atomic.AddUint32(&c.backpressure, ^uint32(0))
		if !c.wouldOverflow(size, bp) {
			// Isolate ourselves from the overflowed callers.
			c.mu.RLock()
			defer c.mu.RUnlock()
		} else {
			c.onceLoop.Do(func() {
				go c.runEvictionLoop()
			})
			c.mu.Lock()
			defer c.mu.Unlock()
			defer func() {
				if atomic.LoadUint32(&c.size) > c.capacity {
					// Cannot decrease backpressure until overflow cleared.
					c.triggerEviction()
					c.cond.Wait()
				}
			}()
			// Trigger an eviction or hitchhike onto an existing request.
			// Cannot continue until overflow cleared.
			c.triggerEviction()
			c.cond.Wait()
		}
	}
	f()
}

func (c *Cache) runEvictionLoop() {
	ticker := time.NewTicker(EvictionInterval)
	defer ticker.Stop()
	for {
		var nowNano int64
		select {
		case <-c.quit:
			return
		case now := <-ticker.C:
			nowNano = now.UnixNano()
		case <-c.evictRequests:
			nowNano = time.Now().UnixNano()
		}
		if c.capacity > 0 {
			// Acquire a read lock to blend
			// in with concurrent readers.
			c.mu.RLock()
			c.processRecords(nowNano)
			c.evictLFU()
			c.cond.Broadcast()
			c.mu.RUnlock()
		} else {
			c.processRecords(nowNano)
		}
	}
}

func (c *Cache) processRecords(now int64) {
	c.records.Range(func(key, value interface{}) bool {
		r := value.(*record)
		r.mu.RLock()
		// An expiry callback might be active
		// or the record was deleted.
		expires := atomic.LoadInt64(&r.expires)
		if expires == math.MaxInt64 || r.isDeleted() {
			r.mu.RUnlock()
			return true
		}
		if expires == 0 || expires > now {
			defer r.mu.RUnlock()
		} else {
			// Set the expiring flag for the next eviction cycle to skip it.
			// The key can be read but not written until evicted or extended.
			atomic.StoreInt64(&r.expires, math.MaxInt64)
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				defer r.mu.RUnlock()
				if c.onExpiry != nil && !c.onExpiry(key, r.value) {
					// Restore the record.
					atomic.StoreInt64(&r.expires, time.Now().Add(r.ttl).UnixNano())
					return
				}
				rec, loaded := c.records.LoadAndDelete(key)
				if !loaded {
					panic("evcache: invariant failed: record does not exist")
				}
				if r != rec.(*record) {
					panic("evcache: invariant failed: record is not the same")
				}
				r.delete()
				c.runAfterEvict(key, r)
				atomic.AddUint32(&c.size, ^uint32(0))
			}()
			return true
		}
		if c.capacity == 0 {
			return true
		}
		hits := atomic.SwapUint32(&r.hits, 0)
		// The elem does not need moving.
		if hits == 0 && r.elem != nil {
			return true
		}
		c.listMu.Lock()
		defer c.listMu.Unlock()
		if r.elem == nil {
			r.elem = c.list.PushBack(key)
		} else if hits > 0 && c.list.Len() > 1 {
			// Update the record's position in the list
			// based on hits delta.
			target := r.elem
			for i := uint32(0); i < hits; i++ {
				if next := target.Next(); next != nil {
					target = next
				} else {
					break
				}
			}
			if target != r.elem {
				c.list.MoveAfter(r.elem, target)
			}
		}
		return true
	})
}

func (c *Cache) evictLFU() {
	pop := func() (key interface{}, success bool) {
		c.listMu.Lock()
		defer c.listMu.Unlock()
		if c.list.Len() > int(c.capacity) {
			return c.list.Remove(c.list.Front()), true
		}
		return nil, false
	}
	for {
		key, ok := pop()
		if !ok {
			return
		}
		c.Evict(key)
	}
}
