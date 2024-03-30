/*
Package backend implements cache backend.
*/
package backend

import (
	"cmp"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Backend implements cache backend.
// The map stores uninitialized or initialized elements, the list stores initialized or removed elements.
type Backend[K comparable, V any] struct {
	Policy           Policy
	timer            *time.Timer
	done             chan struct{}
	xmap             map[K]*Element[V]
	list             []*Element[V]
	pool             sync.Pool
	epoch            atomic.Uint64
	earliestExpireAt int64
	cap              int
	lastLen          int
	len              int
	numDeleted       uint64
	needRealloc      bool
	once             sync.Once
	mu               sync.RWMutex
}

// NewBackend creates a new cache backend.
func NewBackend[K comparable, V any](capacity int) *Backend[K, V] {
	t := time.NewTimer(0)
	<-t.C

	return &Backend[K, V]{
		timer: t,
		done:  make(chan struct{}),
		xmap:  make(map[K]*Element[V], capacity),
		list:  make([]*Element[V], 0, capacity),
		cap:   capacity,
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

	return b.len
}

// Reserve a new uninitialized element.
func (b *Backend[K, V]) Reserve() *Element[V] {
	elem, ok := b.pool.Get().(*Element[V])
	if !ok {
		elem = &Element[V]{}
	}

	elem.wg.Add(1)

	return elem
}

// Release a reserved uninitialized element.
func (b *Backend[K, V]) Release(elem *Element[V]) {
	elem.wg.Done()
	b.pool.Put(elem)
}

// Discard a reserved uninitialized element.
func (*Backend[K, V]) Discard(elem *Element[V]) {
	elem.wg.Done()
}

// Initialize a previously stored uninitialized element.
func (b *Backend[K, V]) Initialize(elem *Element[V], value V, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if elem.initialized {
		panic("Initialize: expected an uninitialized element")
	}

	defer elem.wg.Done()

	elem.Value = value
	if ttl > 0 {
		elem.deadline = time.Now().Add(ttl).UnixNano()
	}

	elem.initialized = true
	b.list = append(b.list, elem)
	b.len++

	if n := b.overflow(); n > 0 {
		b.startGCOnce()
		b.timer.Reset(0)
	} else if elem.deadline > 0 {
		b.startGCOnce()
		if b.earliestExpireAt == 0 || elem.deadline < b.earliestExpireAt {
			b.earliestExpireAt = elem.deadline
			b.timer.Reset(ttl)
		}
	}
}

func (b *Backend[K, V]) incAtomic(elem *Element[V]) {
	switch b.Policy {
	case LFU:
		elem.epoch.Add(1)
	case LRU:
		elem.epoch.Store(b.epoch.Add(1))
	}
}

// Load an initialized element.
func (b *Backend[K, V]) Load(key K) (value *Element[V], ok bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if elem, ok := b.xmap[key]; ok && elem.initialized {
		b.incAtomic(elem)
		return elem, true
	}

	return nil, false
}

// LoadOrStore loads or stores an element.
func (b *Backend[K, V]) LoadOrStore(key K, new *Element[V]) (old *Element[V], initialized, loaded bool) {
	b.mu.RLock()
	if elem, ok := b.xmap[key]; ok {
		defer b.mu.RUnlock()
		if elem.initialized {
			b.incAtomic(elem)
			return elem, true, true
		}
		return elem, false, true
	}
	b.mu.RUnlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	if elem, ok := b.xmap[key]; ok {
		if elem.initialized {
			b.incAtomic(elem)
			return elem, true, true
		}
		return elem, false, true
	}

	// New elements are at epoch 0.
	b.xmap[key] = new

	return new, false, false
}

// Range iterates over initialized cache elements in no particular order.
func (b *Backend[K, V]) Range(f func(key K, r *Element[V]) bool) {
	b.mu.RLock()
	keys := make([]K, 0, len(b.xmap))
	for k := range b.xmap {
		keys = append(keys, k)
	}
	b.mu.RUnlock()

	for _, key := range keys {
		b.mu.RLock()
		elem, ok := b.xmap[key]
		initialized := ok && elem.initialized
		b.mu.RUnlock()
		if initialized && !f(key, elem) {
			return
		}
	}
}

// Evict an element.
func (b *Backend[K, V]) Evict(key K) (*Element[V], bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if elem, ok := b.xmap[key]; ok && elem.initialized {
		b.deleteLocked(key, elem)
		return elem, true
	}

	return nil, false
}

// Delete an element from the backend map.
// The element may be uninitialized.
func (b *Backend[K, V]) Delete(key K) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.deleteLocked(key, nil)
}

// overflow returns the number of overflowed elements.
// Note: b.list must not contain nil elements.
func (b *Backend[K, V]) overflow() int {
	if b.cap > 0 && b.len > b.cap {
		return b.len - b.cap
	}
	return 0
}

func (b *Backend[K, V]) deleteLocked(key K, elem *Element[V]) {
	if elem == nil {
		elem = b.xmap[key]
	}

	if elem != nil {
		delete(b.xmap, key)

		if elem.initialized {
			b.list[slices.Index(b.list, elem)] = nil
			b.len--
			b.numDeleted++
		}
	}

	if b.lastLen == 0 {
		b.lastLen = b.len
	}

	if b.numDeleted > uint64(b.lastLen)/2 {
		b.numDeleted = 0
		b.lastLen = b.len
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
					b.runGC(now.UnixNano())
				}
			}
		}()
	})
}

func (b *Backend[K, V]) sort() {
	largestEpoch := uint64(0)

	slices.SortFunc(b.list, func(a, b *Element[V]) int {
		// Leave nils at the beginning.
		if a == nil && b != nil {
			return -1
		} else if a == nil && b == nil {
			return 0
		} else if a != nil && b == nil {
			return 1
		}

		epochA := a.epoch.Load()
		epochB := b.epoch.Load()
		if epochA > largestEpoch {
			largestEpoch = epochA
		}
		if epochB > largestEpoch {
			largestEpoch = epochB
		}

		result := cmp.Compare(epochA, epochB)

		// Leave newly added elements at the end.
		if epochA == 0 || epochB == 0 {
			return result * -1
		}

		return result
	})

	// Set largest epoch + 1 for new elements.
	for i := len(b.list) - 1; i >= 0; i-- {
		if b.list[i] == nil || !b.list[i].epoch.CompareAndSwap(0, largestEpoch+1) {
			break
		}
	}
}

func (b *Backend[K, V]) collectOverflowed() map[*Element[V]]bool {
	var overflowed map[*Element[V]]bool

	if numOverflowed := b.overflow(); numOverflowed > 0 {
		overflowed = make(map[*Element[V]]bool, numOverflowed)

		for _, elem := range b.list {
			if elem != nil {
				overflowed[elem] = true
				if len(overflowed) == numOverflowed {
					break
				}
			}
		}
	}

	return overflowed
}

func (b *Backend[K, V]) runGC(now int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.Policy {
	case LFU, LRU:
		b.sort()
	}

	overflowed := b.collectOverflowed()

	var (
		newMap  map[K]*Element[V]
		newList []*Element[V]
	)

	if b.needRealloc {
		b.needRealloc = false
		newMap = make(map[K]*Element[V], len(b.xmap)-len(overflowed))
		newList = make([]*Element[V], 0, len(b.xmap)-len(overflowed))
		defer func() {
			b.xmap = newMap
			b.list = newList
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
		if elem.initialized {
			if len(overflowed) > 0 && overflowed[elem] {
				delete(overflowed, elem)
				b.deleteLocked(key, elem)
				continue
			}

			deadline := elem.deadline

			if deadline > 0 && deadline < now {
				b.deleteLocked(key, elem)
				continue
			}

			if deadline > 0 && (earliest == 0 || deadline < earliest) {
				earliest = deadline
			}
		}

		if newMap != nil {
			newMap[key] = elem
		}
	}

	if newList != nil {
		if first := slices.IndexFunc(b.list, func(e *Element[V]) bool {
			return e != nil
		}); first != -1 {
			newList = append(newList, b.list[first:]...)
		}
	}
}
