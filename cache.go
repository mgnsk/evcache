package evcache

import (
	"runtime"
	"sync"
	"time"
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
		b.Close()
	})

	return c
}

// Exists returns whether a value in the cache exists for key.
func (c *Cache[K, V]) Exists(key K) bool {
	r, ok := c.backend.Load(key)
	return ok && r.(*record[V]).initialized.Load()
}

// Get returns the value stored in the cache for key.
func (c *Cache[K, V]) Get(key K) (value V, exists bool) {
	if rec, ok := c.backend.Load(key); ok {
		if r := rec.(*record[V]); r.initialized.Load() {
			return r.value, true
		}
	}

	var zero V
	return zero, false
}

// Range calls f for each key and value present in the cache in no particular order or consistency.
// If f returns false, Range stops the iteration. It skips values that are currently being Fetched.
//
// Range is allowed to modify the cache.
func (c *Cache[K, V]) Range(f func(key K, value V) bool) {
	c.backend.Range(func(key, r any) bool {
		if r := r.(*record[V]); r.initialized.Load() {
			return f(key.(K), r.value)
		}
		return true
	})
}

// Len returns the number of keys in the cache.
func (c *Cache[K, V]) Len() int {
	return c.backend.Len()
}

// Evict a key and return its value.
func (c *Cache[K, V]) Evict(key K) (value V, ok bool) {
	if r, ok := c.backend.Evict(key); ok {
		return r.value, true
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

// MustFetch fetches a value without handling errors. It is safe to panic in f.
func (c *Cache[K, V]) MustFetch(key K, ttl time.Duration, f func() V) (value V) {
	v, _ := c.TryFetch(key, func() (V, time.Duration, error) {
		value := f()
		return value, ttl, nil
	})
	return v
}

// Fetch loads or stores a value for key. If a value exists, f will not be called,
// otherwise f will be called to fetch the new value. It is safe to panic in f.
// Concurrent Fetches for the same key will block each other and return a single result.
func (c *Cache[K, V]) Fetch(key K, ttl time.Duration, f func() (V, error)) (value V, err error) {
	return c.TryFetch(key, func() (V, time.Duration, error) {
		value, err := f()
		return value, ttl, err
	})
}

// TryFetch is like Fetch but allows the TTL to be returned alongside the value from callback.
func (c *Cache[K, V]) TryFetch(key K, f func() (V, time.Duration, error)) (value V, err error) {
	new, ok := c.pool.Get().(*record[V])
	if !ok {
		new = newRecord[V]()
	}

	new.wg.Add(1)
	defer new.wg.Done()

loadOrStore:
	if old, loaded := c.backend.LoadOrStore(key, new); loaded {
		switch r := old.(*record[V]); r.initialized.Load() {
		case true:
			c.pool.Put(new)
			return r.value, nil

		case false:
			r.wg.Wait()
			if r.initialized.Load() {
				c.pool.Put(new)
				return r.value, nil
			}

			goto loadOrStore
		}
	}

	defer func() {
		if r := recover(); r != nil {
			c.backend.Delete(key)

			panic(r)
		}
	}()

	value, ttl, err := f()
	if err != nil {
		c.backend.Delete(key)

		var zero V
		return zero, err
	}

	new.value = value
	if ttl > 0 {
		new.deadline = time.Now().Add(ttl).UnixNano()
	}

	c.backend.PushBack(new, ttl)

	return value, nil
}
