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
// It is the interval for which the LFU order lags behind
// hit statistics of each record which is used to
// relatively move the records.
//
// Insert and delete are immediate.
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

// Cache is a non-blocking eventually consistent LFU cache.
//
// All methods except Close are safe to use concurrently.
type Cache struct {
	records      sync.Map
	pool         sync.Pool
	wg           sync.WaitGroup
	capacity     uint32
	backpressure uint32
	size         uint32
	list         *lfuRing
	afterEvict   EvictionCallback
	quit         chan struct{}
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
				r := ring.New(1)
				r.Value = &record{}
				return r
			},
		}
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
		c.capacity = capacity
	}
}

// Build the cache.
func (build Builder) Build() *Cache {
	c := &Cache{}
	build(c)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(SyncInterval)
		defer ticker.Stop()
		for {
			var nowNano int64
			select {
			case <-c.quit:
				return
			case now := <-ticker.C:
				nowNano = now.UnixNano()
			}
			c.processRecords(nowNano)
			if c.capacity > 0 {
				c.evictLFU()
			}
		}
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
		oldring := old.(*ring.Ring)
		oldrec := oldring.Value.(*record)
		oldrec.mu.RLock()
		if oldrec.valid() {
			defer oldrec.mu.RUnlock()
			return oldrec.load(), oldrec, true
		}
		oldrec.mu.RUnlock()
		c.evict(key)
	}
}

// Fetch attempts to get or set the value and calls f on a miss to receive the new value.
// If f returns an error, no value is cached and the error is returned back to caller.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
func (c *Cache) Fetch(key interface{}, ttl time.Duration, f FetchCallback) (value interface{}, closer io.Closer, err error) {
	if c.capacity > 0 {
		bp := c.acquire()
		defer c.release(bp)
	}
	for {
		newring := c.pool.Get().(*ring.Ring)
		newrec := newring.Value.(*record)
		newrec.mu.Lock()
		old, loaded := c.records.LoadOrStore(key, newring)
		if !loaded {
			defer newrec.mu.Unlock()
			value, err := f()
			if err != nil {
				newrec.softDelete()
				return nil, nil, err
			}
			newrec.wg.Add(1)
			newrec.hits = 1
			c.initRecord(newring, key, value, ttl)
			atomic.AddUint32(&c.size, 1)
			return value, newrec, nil
		}
		newrec.mu.Unlock()
		c.pool.Put(newring)

		oldring := old.(*ring.Ring)
		if oldring == newring {
			panic("evcache: must have loaded a different ring")
		}
		oldrec := oldring.Value.(*record)
		oldrec.mu.RLock()
		if oldrec.valid() {
			defer oldrec.mu.RUnlock()
			return oldrec.load(), oldrec, nil
		}
		oldrec.mu.RUnlock()
		c.evict(key)
	}
}

// Set the value for a key with a timeout.
func (c *Cache) Set(key, value interface{}, ttl time.Duration) {
	if c.capacity > 0 {
		bp := c.acquire()
		defer c.release(bp)
	}
	for {
		newring := c.pool.Get().(*ring.Ring)
		newrec := newring.Value.(*record)
		newrec.mu.Lock()
		_, loaded := c.records.LoadOrStore(key, newring)
		if !loaded {
			defer newrec.mu.Unlock()
			c.initRecord(newring, key, value, ttl)
			atomic.AddUint32(&c.size, 1)
			return
		}
		newrec.mu.Unlock()
		c.pool.Put(newring)
		c.evict(key)
	}
}

// Evict a key and return its value if it exists.
func (c *Cache) Evict(key interface{}) (value interface{}, evicted bool) {
	return c.evict(key)
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
	return int(atomic.LoadUint32(&c.size))
}

// Close shuts down the cache, evicts all keys
// and waits for eviction callbacks to finish.
//
// It is not safe to close the cache
// while in use.
func (c *Cache) Close() error {
	close(c.quit)
	c.wg.Wait()
	c.records.Range(func(key, _ interface{}) bool {
		c.Evict(key)
		return true
	})
	c.wg.Wait()
	return nil
}

func (c *Cache) acquire() uint32 {
	return atomic.AddUint32(&c.backpressure, 1)
}

func (c *Cache) release(bp uint32) {
	if c.wouldOverflow(bp) {
		c.evictLFU()
	}
	atomic.AddUint32(&c.backpressure, ^uint32(0))
}

func (c *Cache) wouldOverflow(bp uint32) bool {
	size := atomic.LoadUint32(&c.size)
	return size > c.capacity && size-c.capacity > bp
}

func (c *Cache) evict(key interface{}) (value interface{}, evicted bool) {
	value, loaded := c.records.LoadAndDelete(key)
	if !loaded {
		return nil, false
	}
	r := value.(*ring.Ring)
	rec := r.Value.(*record)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.once.Do(func() {
		value = rec.value
		evicted = true
		if rec.softDelete() {
			atomic.AddUint32(&c.size, ^uint32(0))
			if c.capacity > 0 {
				c.list.remove(r)
			}
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			rec.wg.Wait()
			if c.afterEvict != nil {
				c.afterEvict(key, value)
			}
		}()
	})

	return value, evicted
}

func (c *Cache) initRecord(r *ring.Ring, key, value interface{}, ttl time.Duration) {
	rec := r.Value.(*record)
	rec.value = value
	if ttl > 0 {
		rec.expires = time.Now().Add(ttl).UnixNano()
	}
	if c.capacity > 0 {
		c.list.insert(r)
	}
}

func (c *Cache) evictLFU() {
	for {
		rec := c.list.pop()
		if rec == nil {
			break
		}
		if rec.softDelete() {
			atomic.AddUint32(&c.size, ^uint32(0))
		}
	}
}

func (c *Cache) processRecords(now int64) {
	c.records.Range(func(key, value interface{}) bool {
		r := value.(*ring.Ring)
		rec := r.Value.(*record)
		rec.mu.RLock()
		defer rec.mu.RUnlock()
		if !rec.valid() || rec.expires > 0 && rec.expires < now {
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				c.evict(key)
			}()
			return true
		}
		if c.capacity == 0 {
			return true
		}
		if hits := atomic.SwapUint32(&rec.hits, 0); hits > 0 {
			c.list.promote(r, hits)
		}
		return true
	})
}
