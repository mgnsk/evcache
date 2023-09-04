package evcache

import (
	"runtime"
	"sync"
	"time"

	"github.com/mgnsk/list"
)

// Cache is an in-memory TTL cache with optional capacity.
type Cache[K comparable, V any] struct {
	backend *backend[K, V]
	pool    sync.Pool
}

// New creates an empty cache.
func New[K comparable, V any](capacity int) *Cache[K, V] {
	b := newBackend[K, V](capacity)

	c := &Cache[K, V]{
		backend: b,
	}

	runtime.SetFinalizer(c, func(any) {
		b.close()
	})

	return c
}

// Exists returns whether a value in the cache exists for key.
func (c *Cache[K, V]) Exists(key K) bool {
	c.backend.mu.RLock()
	defer c.backend.mu.RUnlock()

	elem, ok := c.backend.xmap.Load(key)
	return ok && elem.Value.initialized.Load()
}

// Get returns the value stored in the cache for key.
func (c *Cache[K, V]) Get(key K) (value V, exists bool) {
	c.backend.mu.RLock()
	defer c.backend.mu.RUnlock()

	if elem, ok := c.backend.xmap.Load(key); ok && elem.Value.initialized.Load() {
		return elem.Value.value, true
	}

	var zero V
	return zero, false
}

// Range calls f for each key and value present in the cache in no particular order or consistency.
// If f returns false, Range stops the iteration. It skips values that are currently being Fetched.
//
// Range is allowed to modify the cache.
func (c *Cache[K, V]) Range(f func(key K, value V) bool) {
	c.backend.Range(func(key K, elem *list.Element[record[V]]) bool {
		if elem.Value.initialized.Load() {
			return f(key, elem.Value.value)
		}
		return true
	})
}

// Len returns the number of keys in the cache.
func (c *Cache[K, V]) Len() int {
	c.backend.mu.Lock()
	defer c.backend.mu.Unlock()

	return c.backend.list.Len()
}

// Evict a key and return its value.
func (c *Cache[K, V]) Evict(key K) (value V, ok bool) {
	c.backend.mu.Lock()
	defer c.backend.mu.Unlock()

	if elem, ok := c.backend.evict(key); ok {
		return elem.Value.value, true
	}

	var zero V
	return zero, false
}

// LoadOrStore loads or stores a value for key. If the key is being Fetched, LoadOrStore
// blocks until Fetch returns.
func (c *Cache[K, V]) LoadOrStore(key K, ttl time.Duration, value V) (old V, loaded bool) {
	loaded = true

	v, _ := c.Fetch(key, ttl, func() (V, error) {
		loaded = false
		return value, nil
	})

	return v, loaded
}

// MustFetch fetches a value or panics if f panics.
func (c *Cache[K, V]) MustFetch(key K, ttl time.Duration, f func() V) (value V) {
	v, _ := c.TryFetch(key, func() (V, time.Duration, error) {
		value := f()
		return value, ttl, nil
	})
	return v
}

// Fetch loads or stores a value for key. If a value exists, f will not be called,
// otherwise f will be called to fetch the new value. It panics if f panics.
// Concurrent Fetches for the same key will block each other and return a single result.
func (c *Cache[K, V]) Fetch(key K, ttl time.Duration, f func() (V, error)) (value V, err error) {
	return c.TryFetch(key, func() (V, time.Duration, error) {
		value, err := f()
		return value, ttl, err
	})
}

// TryFetch is like Fetch but allows the TTL to be returned alongside the value from callback.
func (c *Cache[K, V]) TryFetch(key K, f func() (V, time.Duration, error)) (value V, err error) {
	new, ok := c.pool.Get().(*list.Element[record[V]])
	if !ok {
		new = list.NewElement(record[V]{})
	}

	new.Value.wg.Add(1)
	defer new.Value.wg.Done()

loadOrStore:
	if elem, loaded := c.backend.LoadOrStore(key, new); loaded {
		if elem.Value.initialized.Load() {
			c.pool.Put(new)
			return elem.Value.value, nil
		}

		elem.Value.wg.Wait()

		if elem.Value.initialized.Load() {
			c.pool.Put(new)
			return elem.Value.value, nil
		}

		goto loadOrStore
	}

	defer func() {
		if r := recover(); r != nil {
			c.backend.mu.Lock()
			defer c.backend.mu.Unlock()

			c.backend.xmap.Delete(key)

			panic(r)
		}
	}()

	value, ttl, err := f()
	if err != nil {
		c.backend.mu.Lock()
		defer c.backend.mu.Unlock()

		c.backend.xmap.Delete(key)

		var zero V
		return zero, err
	}

	new.Value.value = value
	if ttl > 0 {
		new.Value.deadline = time.Now().Add(ttl).UnixNano()
	}

	c.backend.mu.Lock()
	defer c.backend.mu.Unlock()

	c.backend.pushBack(new, ttl)

	return value, nil
}
