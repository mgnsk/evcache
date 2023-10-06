/*
Package backend implements cache backend.
*/
package backend

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/mgnsk/list"
)

type record[V any] struct {
	value       V
	deadline    int64
	wg          sync.WaitGroup
	initialized atomic.Bool
}

func (r *record[V]) Value() V {
	return r.value
}

func (r *record[V]) Wait() {
	r.wg.Wait()
}

// Element is the cache element.
type Element[V any] *list.Element[record[V]]

// RecordMap is the cache's element map.
type RecordMap[K comparable, V any] map[K]Element[V]

// Backend implements cache backend.
type Backend[K comparable, V any] struct {
	timer            *time.Timer
	done             chan struct{}
	xmap             RecordMap[K, V]
	list             list.List[record[V]]
	pool             sync.Pool
	earliestExpireAt int64
	cap              int
	reallocThreshold int // if map hits this size and then shrinks by half, it is reallocated
	largestLen       int // the map has at least this capacity
	needRealloc      bool
	once             sync.Once
	mu               sync.RWMutex
}

// NewBackend creates a new cache backend.
func NewBackend[K comparable, V any](capacity int) *Backend[K, V] {
	t := time.NewTimer(0)
	<-t.C

	return &Backend[K, V]{
		timer:            t,
		done:             make(chan struct{}),
		xmap:             make(RecordMap[K, V], capacity),
		cap:              capacity,
		reallocThreshold: 100000, // 100000 * pointer size
	}
}

// Close stops the backend cleanup loop.
func (b *Backend[K, V]) Close() error {
	close(b.done)
	return nil
}

// Len returns the number of initialized elements.
func (b *Backend[K, V]) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.list.Len()
}

// Reserve a new uninitialized element.
func (b *Backend[K, V]) Reserve() Element[V] {
	elem, ok := b.pool.Get().(Element[V])
	if !ok {
		elem = list.NewElement(record[V]{value: *new(V)})
	}

	elem.Value.wg.Add(1)

	return elem
}

// Release a reserved uninitialized element.
func (b *Backend[K, V]) Release(elem Element[V]) {
	elem.Value.wg.Done()
	b.pool.Put(elem)
}

// Initialize a previously stored uninitialized element.
func (b *Backend[K, V]) Initialize(key K, value V, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	elem, ok := b.xmap[key]
	if !ok {
		panic("Initialize: expected element to exist")
	}

	elem.Value.value = value
	if ttl > 0 {
		elem.Value.deadline = time.Now().Add(ttl).UnixNano()
	}

	b.list.PushBack(elem)
	if !elem.Value.initialized.CompareAndSwap(false, true) {
		panic("Initialize: expected an uninitialized element")
	}

	defer elem.Value.wg.Done()

	if n := b.overflow(); n > 0 {
		b.startGCOnce()
		b.timer.Reset(0)
	} else if elem.Value.deadline > 0 {
		b.startGCOnce()
		if b.earliestExpireAt == 0 || elem.Value.deadline < b.earliestExpireAt {
			b.earliestExpireAt = elem.Value.deadline
			b.timer.Reset(ttl)
		}
	}
}

// Load an initialized element.
func (b *Backend[K, V]) Load(key K) (value Element[V], ok bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if r, ok := b.xmap[key]; ok && r.Value.initialized.Load() {
		return r, true
	}

	return nil, false
}

// LoadOrStore loads or stores an element.
func (b *Backend[K, V]) LoadOrStore(key K, new Element[V]) (old Element[V], initialized, loaded bool) {
	b.mu.RLock()
	if elem, ok := b.xmap[key]; ok {
		b.mu.RUnlock()
		return elem, elem.Value.initialized.Load(), true
	}
	b.mu.RUnlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	if elem, ok := b.xmap[key]; ok {
		return elem, elem.Value.initialized.Load(), true
	}

	b.xmap[key] = new

	return new, false, false
}

// Range iterates over initialized cache elements in no particular order.
func (b *Backend[K, V]) Range(f func(key K, r Element[V]) bool) {
	b.mu.RLock()
	keys := make([]K, 0, len(b.xmap))
	for k := range b.xmap {
		keys = append(keys, k)
	}
	b.mu.RUnlock()

	for _, key := range keys {
		b.mu.RLock()
		elem, ok := b.xmap[key]
		b.mu.RUnlock()
		if ok && elem.Value.initialized.Load() && !f(key, elem) {
			return
		}
	}
}

// Evict an element.
func (b *Backend[K, V]) Evict(key K) (Element[V], bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if elem, ok := b.xmap[key]; ok && elem.Value.initialized.Load() {
		b.deleteLocked(key)
		b.list.Remove(elem)
		return elem, true
	}

	return nil, false
}

// Delete an element from the backend map.
func (b *Backend[K, V]) Delete(key K) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.deleteLocked(key)
}

func (b *Backend[K, V]) deleteLocked(key K) {
	if b.cap == 0 {
		if n := len(b.xmap); n >= b.reallocThreshold || b.largestLen > 0 && n > b.largestLen {
			b.largestLen = n
		}
	}

	delete(b.xmap, key)

	if b.largestLen > 0 && len(b.xmap) <= b.largestLen/2 {
		b.largestLen = 0
		b.needRealloc = true
		b.timer.Reset(0)
	}
}

func (b *Backend[K, V]) startGCOnce() {
	b.once.Do(func() {
		go func() {
			for {
				select {
				case <-b.done:
					b.timer.Stop()
					return
				case now := <-b.timer.C:
					b.RunGC(now.UnixNano())
				}
			}
		}()
	})
}

// RunGC runs map cleanup.
func (b *Backend[K, V]) RunGC(now int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var overflowed map[Element[V]]bool
	if n := b.overflow(); n > 0 {
		overflowed = make(map[Element[V]]bool, n)

		elem := b.list.Front()
		overflowed[elem] = true

		for i := 1; i < n; i++ {
			elem = elem.Next()
			overflowed[elem] = true
		}
	}

	var newMap RecordMap[K, V]
	if b.needRealloc {
		b.needRealloc = false
		newMap = make(RecordMap[K, V], len(b.xmap)-len(overflowed))
		defer func() {
			b.xmap = newMap
		}()
	}

	var earliest int64
	defer func() {
		b.earliestExpireAt = earliest
		if earliest > 0 {
			b.timer.Reset(time.Duration(earliest - now))
		}
	}()

	for key, elem := range b.xmap {
		if elem.Value.initialized.Load() {
			if len(overflowed) > 0 && overflowed[elem] {
				delete(overflowed, elem)
				b.deleteLocked(key)
				b.list.Remove(elem)
				continue
			}

			if elem.Value.deadline > 0 && elem.Value.deadline < now {
				b.deleteLocked(key)
				b.list.Remove(elem)
				continue
			}

			if elem.Value.deadline > 0 && (earliest == 0 || elem.Value.deadline < earliest) {
				earliest = elem.Value.deadline
			}
		}

		if newMap != nil {
			newMap[key] = elem
		}
	}
}

func (b *Backend[K, V]) overflow() int {
	if b.cap > 0 && b.list.Len() > b.cap {
		return b.list.Len() - b.cap
	}
	return 0
}
