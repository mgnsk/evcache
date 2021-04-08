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
// It runs after all transactions for key have finished.
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
//
// Exists is a non-blocking operation.
func (c *Cache) Exists(key interface{}) bool {
	if r, ok := c.records.Load(key); ok && r.(*record).State() == active {
		return true
	}
	return false
}

// Get returns the value stored in the cache for a key. The boolean indicates
// whether a value was found.
//
// When the returned value is not used anymore, the caller MUST call closer.Close()
// or a memory leak will occur.
//
// Get is a non-blocking operation.
func (c *Cache) Get(key interface{}) (value interface{}, closer io.Closer, exists bool) {
	if r, ok := c.records.Load(key); ok && r.(*record).State() == active {
		r := r.(*record)
		if value, ok = r.LoadAndHit(); ok {
			return value, r, true
		}
	}
	return nil, nil, false
}

// Range calls f for each key and value present in the cache in no particular order.
// If f returns false, Range stops the iteration.
//
// Range does not necessarily correspond to any consistent snapshot of the cache's
// contents: no key will be visited more than once, but if the value for any key
// is stored or deleted concurrently, Range may reflect any mapping for that key
// from any point during the Range call.
//
// Range is a non-blocking operation.
func (c *Cache) Range(f func(key, value interface{}) bool) {
	c.records.Range(func(key, r interface{}) bool {
		if v, ok := r.(*record).TryLoad(); ok {
			return f(key, v)
		}
		return true
	})
}

// Len returns the number of keys in the cache.
//
// Len does not block.
func (c *Cache) Len() int {
	return c.list.Len()
}

// Pop evicts and returns the oldest key and value. If LFU ordering is
// enabled, then the least frequently used key and value.
//
// Pop may block if a concurrent Do is running.
func (c *Cache) Pop() (key, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key = c.list.Pop(); key == nil {
		return nil, nil
	}
	value, ok := c.deleteLocked(key, nil)
	if !ok {
		return nil, nil
	}
	return key, value
}

// Flush evicts all keys from the cache.
//
// Flush may block if a concurrent Do is running.
func (c *Cache) Flush() {
	c.records.Range(func(key, _ interface{}) bool {
		c.Evict(key)
		return true
	})
}

// Evict evicts a key and returns its value.
//
// Evict may block if a concurrent Do is running.
func (c *Cache) Evict(key interface{}) (value interface{}, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deleteLocked(key, nil)
}

// CompareAndEvict evicts the key only if its value is deeply equal to old.
// It is the only way to safely evict records in ModeNonBlocking
// under concurrent use.
//
// This method is safe only if value is unique. If this is not the case,
// such as when values are pooled then the user must make sure that this
// method returns before putting the value back into the pool in the presence
// of concurrent writers for key where the new value originates from the pool.
//
// CompareAndEvict may block if a concurrent Do is running.
func (c *Cache) CompareAndEvict(key, value interface{}) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.deleteLocked(key, &value)
	return ok
}

// Set the value in the cache for a key.
//
// If the cache has a capacity limit and it is exceeded,
// the least frequently used record or the eldest is evicted
// depending on whether LFU ordering is enabled or not.
//
// Set may block until a concurrent Fetch callback for key has returned
// and then immediately overwrite the value. It also blocks when
// a concurrent Do is running.
//
// If ModeBlocking is used, it blocks until all transactions and
// eviction callback have finished for key.
func (c *Cache) Set(key, value interface{}, ttl time.Duration) {
	var front interface{}
	defer func() {
		if front != nil {
			c.Evict(front)
		}
	}()
	if ttl > 0 {
		c.runLoopOnce()
	}
	doEvict := func(r *record) {
		switch r.State() {
		case evicting:
			if c.mode == ModeBlocking {
				r.evictionWg.Wait()
			}
		case active:
			c.Evict(key)
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
			front = c.list.PushBack(key, new.ring)
			new.Init(value, ttl)
			new.SetState(active)
			return
		}
		doEvict(old.(*record))
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
//
// Fetch may block until a concurrent Fetch callback for key has returned and will return
// that new value or continues with fetching a new value if the concurrent callback
// returned an error. It also blocks if a concurrent Do is running and the value
// did not exist causing a store.
//
// If ModeBlocking is used and the value is being evicted, it blocks until
// all transactions and eviction callback have finished for key.
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
		if ttl > 0 {
			c.runLoopOnce()
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		front = c.list.PushBack(key, new.ring)
		new.readerWg.Add(1)
		new.Init(value, ttl)
		new.SetState(active)
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

// Do calls f sequentially for each key and value present in the cache
// in insertion order by default. When LFU is used, the order is
// from least to most frequently used. If f returns false,
// Do stops the iteration.
//
// Do blocks Pop, Evict, Set and Fetch (only if it stores a value).
// It does not visit keys whose Fetch or eviction callback is
// currently running. f is not allowed to modify the cache.
func (c *Cache) Do(f func(key, value interface{}) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list.Do(func(key interface{}) bool {
		r, ok := c.records.Load(key)
		if !ok {
			panic("evcache: invalid map state")
		}
		value := r.(*record).value
		return f(key, value)
	})
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

func (c *Cache) deleteLocked(key interface{}, target *interface{}) (value interface{}, ok bool) {
	rec, ok := c.records.Load(key)
	if !ok {
		return nil, false
	}
	r := rec.(*record)
	if r.State() != active {
		return nil, false
	}
	value = r.value
	if target != nil && !reflect.DeepEqual(value, *target) {
		return nil, false
	}
	switch c.mode {
	case ModeNonBlocking:
		// In non-blocking mode, new writers see an empty map immediately.
		c.records.Delete(key)
	case ModeBlocking:
		// In blocking mode, new writers load and wait for the old record.
		r.evictionWg.Add(1)
		r.SetState(evicting)
	}
	if k := c.list.Remove(r.ring); k != nil && k != key {
		panic("evcache: invalid ring")
	}
	if c.mode == ModeNonBlocking && c.afterEvict == nil {
		return value, true
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if c.mode == ModeBlocking {
			defer r.evictionWg.Done()
			defer c.records.Delete(key)
		}
		r.readerWg.Wait()
		if c.afterEvict != nil {
			c.afterEvict(key, value)
		}
	}()
	return value, true
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
			// Record is being fetched or eviction callback running.
			return true
		}
		if r.Expired(now) {
			c.deleteLocked(key, nil)
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
