package evcache

import (
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

// SyncInterval is the interval for background loop
// which runs when autoexpiry or LFU is enabled.
//
// If cache overflows while LFU is enabled and records are created faster than
// SyncInterval can update record ordering, the eviction starts losing LFU order
// and will become the insertion order with eldest first.
var SyncInterval = time.Second

// FetchCallback is called when *Cache.Fetch has a miss
// and must return a new value or an error.
//
// It blocks the key from being Set or Fetched concurrently.
type FetchCallback func() (interface{}, error)

// EvictionCallback is guaranteed to run in a new goroutine when a cache record is evicted.
//
// It runs after all transactions for key have finished regardless of eviction mode.
type EvictionCallback func(key, value interface{})

// EvictionMode specifies how transactions and eviction callback behave.
//
// A transaction is the io.Closer returned with each record.
type EvictionMode int

const (
	// ModeNonBlocking allows keys to be overwritten while having active transactions.
	//
	// This is the default mode.
	ModeNonBlocking EvictionMode = iota

	// ModeBlocking configures transactions and EvictionCallback
	// to block writers for the same key after being evicted.
	ModeBlocking
)

// Cache is an in-memory ordered cache.
type Cache struct {
	records    sync.Map
	pool       sync.Pool
	mu         sync.Mutex
	onceLoop   sync.Once
	wg         sync.WaitGroup
	lfuEnabled bool
	list       *ringList
	afterEvict EvictionCallback
	mode       EvictionMode
	stopLoop   chan struct{}
}

// Builder builds a cache.
type Builder func(*Cache)

// New creates an empty cache.
//
// Cache must be closed after usage has stopped
// to prevent leaking resources.
//
// It is not safe to close the cache while in use.
func New() Builder {
	return func(c *Cache) {}
}

// WithEvictionCallback specifies an asynchronous eviction callback.
// After record is evicted from cache, the callback is guaranteed to run.
func (build Builder) WithEvictionCallback(cb EvictionCallback) Builder {
	return func(c *Cache) {
		build(c)
		c.afterEvict = cb
	}
}

// WithEvictionMode specifies an eviction blocking mode.
//
// The default mode is ModeNonBlocking.
func (build Builder) WithEvictionMode(mode EvictionMode) Builder {
	return func(c *Cache) {
		build(c)
		switch mode {
		case ModeBlocking:
		case ModeNonBlocking:
		default:
			panic("evcache: invalid mode")
		}
		c.mode = mode
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
		c.runLoopOnce()
	}
	return c
}

// Exists returns whether a value in the cache exists for key.
// It does not block.
func (c *Cache) Exists(key interface{}) bool {
	r, ok := c.records.Load(key)
	return ok && r.(*record).State() == active
}

// Get returns the value stored in the cache for a key and begins a transaction.
// The boolean indicates whether a value was found. It does not block
// and returns a zero result when FetchCallback for key runs.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
func (c *Cache) Get(key interface{}) (value interface{}, closer io.Closer, exists bool) {
	if r, ok := c.records.Load(key); ok && r.(*record).State() == active {
		if value, ok = r.(*record).LoadAndHit(); ok {
			return value, r.(io.Closer), true
		}
	}
	return nil, nil, false
}

// Range calls f for each key and value present in the cache in no particular order.
// If f returns false, Range stops the iteration. It does not block and skips
// keys with a pending FetchCallback. Range is allowed to modify the cache.
//
// Range does not necessarily correspond to any consistent snapshot of the cache's
// contents: no key will be visited more than once, but if the value for any key
// is stored or deleted concurrently, Range may reflect any mapping for that key
// from any point during the Range call.
func (c *Cache) Range(f func(key, value interface{}) bool) {
	c.records.Range(func(key, r interface{}) bool {
		if r := r.(*record); r.State() == active {
			// Active records have read-only values.
			return f(key, r.value)
		}
		return true
	})
}

// Len returns the number of keys in the cache.
func (c *Cache) Len() int {
	return c.list.Len()
}

// Do calls f sequentially for each key and value present in the cache, by default
// in insertion order. In LFU mode, keys are visited from least to most frequently
// accessed. If f returns false, the iteration stops.
//
// Do blocks writers and skips keys with a pending FetchCallback.
// f is not allowed to modify the cache unless it does so
// in a new goroutine.
func (c *Cache) Do(f func(key, value interface{}) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list.Do(func(key interface{}) bool {
		r, ok := c.records.Load(key)
		if !ok {
			panic("evcache: invalid cache state")
		}
		return f(key, r.(*record).value)
	})
}

// Pop evicts and returns the first key and value. By default, it returns
// the oldest key. In LFU mode, the least frequently used key is returned.
// It skips keys with a pending FetchCallback.
func (c *Cache) Pop() (key, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key := c.list.Pop(); key != nil {
		if r, ok := c.evictLocked(key, nil); ok {
			return key, r.value
		}
		panic("evcache: invalid cache state")
	}
	return nil, nil
}

// Flush evicts all keys from the cache.
// It skips keys with a pending FetchCallback.
func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records.Range(func(key, _ interface{}) bool {
		if r, ok := c.evictLocked(key, nil); ok {
			c.list.Remove(r.ring)
		}
		return true
	})
}

// Evict evicts a key and returns its value.
// It skips keys with a pending FetchCallback.
func (c *Cache) Evict(key interface{}) (value interface{}, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.evictLocked(key, nil); ok {
		c.list.Remove(r.ring)
		return r.value, true
	}
	return nil, false
}

// CompareAndEvict evicts the key only if its value is deeply equal to old.
// It is safe to use when every new value for key is unique.
//
// If values are recycled using sync.Pool, then in the presence of writers
// for key where the new value originates from the pool, this method
// must return before it is safe to pool the value again.
func (c *Cache) CompareAndEvict(key, value interface{}) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.evictLocked(key, &value); ok {
		c.list.Remove(r.ring)
		return true
	}
	return false
}

// Set the value in the cache for a key.
//
// If the cache has a capacity limit and it is exceeded then depending on mode,
// the eldest or the least frequently used record is evicted.
//
// When overwriting a key, Set may wait for concurrent FetchCallbacks
// to finish and evicts the old key. In ModeBlocking, it waits for
// transactions and EvictionCallback to finish.
func (c *Cache) Set(key, value interface{}, ttl time.Duration) {
	var front interface{}
	defer func() {
		if front != nil {
			c.Evict(front)
		}
	}()
	doEvict := func(r *record) {
		switch r.State() {
		case active:
			c.Evict(key)
		case evicting:
			if c.mode == ModeBlocking {
				r.evictionWg.Wait()
			}
		default:
			defer c.Evict(key)
			// Wait for any pending Fetch callback.
			r.mu.Lock()
			defer r.mu.Unlock()
		}
	}
	new := c.pool.Get().(*record)
	new.mu.Lock()
	defer new.mu.Unlock()
	for {
		old, loaded := c.records.LoadOrStore(key, new)
		if !loaded {
			c.mu.Lock()
			defer c.mu.Unlock()
			new.ring.Value = key
			front = c.list.PushBack(new.ring)
			new.init(value, ttl)
			new.setState(active)
			if ttl > 0 {
				c.runLoopOnce()
			}
			return
		}
		doEvict(old.(*record))
	}
}

// Fetch attempts to get or set the value and calls f on a miss to receive the new value.
// If f returns an error, no value is cached and the error is returned back to caller.
// It begins a transaction for key.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
//
// If the cache has a capacity limit and it is exceeded then depending on mode,
// the eldest or the least frequently used record is evicted.
//
// It waits for concurrent FetchCallbacks for key to finish.
// In ModeBlocking, if the loaded key was concurrently evicted,
// it waits for transactions and EvictionCallback to finish.
//
// If the value exists, Fetch will not block.
func (c *Cache) Fetch(key interface{}, ttl time.Duration, f FetchCallback) (value interface{}, closer io.Closer, err error) {
	var (
		didLoad bool
		front   interface{}
	)
	new := c.pool.Get().(*record)
	defer func() {
		if didLoad {
			c.pool.Put(new)
		} else if front != nil {
			c.Evict(front)
		}
	}()
	new.mu.Lock()
	defer new.mu.Unlock()
	loadOrStore := func() (old *record, loaded bool) {
		if old, loaded := c.records.LoadOrStore(key, new); loaded {
			return old.(*record), true
		}
		value, err = f()
		if err != nil {
			c.records.Delete(key)
			return nil, false
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		new.ring.Value = key
		front = c.list.PushBack(new.ring)
		new.readerWg.Add(1)
		new.init(value, ttl)
		new.setState(active)
		if ttl > 0 {
			c.runLoopOnce()
		}
		return nil, false
	}
	for {
		r, loaded := loadOrStore()
		if err != nil {
			return nil, nil, err
		}
		if !loaded {
			return value, new, nil
		}
		if v, ok := r.LoadAndHit(); ok {
			didLoad = true
			return v, r, nil
		}
		if c.mode == ModeBlocking {
			r.evictionWg.Wait()
		}
	}
}

// Close shuts down the cache, evicts all keys
// and waits for eviction callbacks to finish.
//
// It is not safe to close the cache
// while in use.
func (c *Cache) Close() error {
	c.runLoopOnce()
	c.stopLoop <- struct{}{}
	close(c.stopLoop)
	c.Flush()
	c.wg.Wait()
	return nil
}

func (c *Cache) evictLocked(key interface{}, target *interface{}) (r *record, ok bool) {
	rec, ok := c.records.Load(key)
	if !ok {
		return nil, false
	}
	r = rec.(*record)
	if r.State() != active || target != nil && !reflect.DeepEqual(r.value, *target) {
		return nil, false
	}
	// Safe to lock r.mu on an active record while holding c.mu.
	r.mu.Lock()
	defer r.mu.Unlock()
	switch c.mode {
	case ModeNonBlocking:
		// In non-blocking mode, new writers see an empty map immediately.
		c.records.Delete(key)
	case ModeBlocking:
		// In blocking mode, new writers load and wait for the old record.
		// Add before setState to allow waiters to use an unlocked record.
		r.evictionWg.Add(1)
	}
	r.setState(evicting)
	if c.mode == ModeBlocking || c.afterEvict != nil {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			if c.mode == ModeBlocking {
				// Finish the eviction.
				defer r.evictionWg.Done()
				defer c.records.Delete(key)
			}
			r.readerWg.Wait()
			if c.afterEvict != nil {
				c.afterEvict(key, r.value)
			}
		}()
	}
	return r, true
}

func (c *Cache) runLoopOnce() {
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
		if r.State() != active {
			return true
		}
		if deadline := r.Deadline(); deadline > 0 && deadline < now {
			c.list.Remove(r.ring)
			c.evictLocked(key, nil)
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
