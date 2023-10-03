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

// Record is a cache record.
type Record[V any] struct {
	Value       V
	Deadline    int64
	Wg          sync.WaitGroup
	Initialized atomic.Bool
}

// Backend implements cache backend.
type Backend[K comparable, V any] struct {
	timer            *time.Timer
	done             chan struct{}
	xmap             map[K]*list.Element[Record[V]]
	list             list.List[Record[V]]
	earliestExpireAt int64
	cap              int
	once             sync.Once
	sync.RWMutex
}

// NewBackend creates a new cache backend.
func NewBackend[K comparable, V any](capacity int) *Backend[K, V] {
	t := time.NewTimer(0)
	<-t.C

	return &Backend[K, V]{
		timer: t,
		done:  make(chan struct{}),
		xmap:  make(map[K]*list.Element[Record[V]], capacity),
		cap:   capacity,
	}
}

// Close stops the backend cleanup loop.
func (b *Backend[K, V]) Close() error {
	close(b.done)
	return nil
}

// Len returns the number of stored records.
func (b *Backend[K, V]) Len() int {
	return b.list.Len()
}

// Load an element.
func (b *Backend[K, V]) Load(key K) (value *list.Element[Record[V]], ok bool) {
	r, ok := b.xmap[key]
	return r, ok
}

// LoadOrStore loads or stores an element.
func (b *Backend[K, V]) LoadOrStore(key K, new *list.Element[Record[V]]) (old *list.Element[Record[V]], loaded bool) {
	b.RLock()
	if elem, ok := b.xmap[key]; ok {
		b.RUnlock()
		return elem, true
	}
	b.RUnlock()

	b.Lock()
	defer b.Unlock()

	if elem, ok := b.xmap[key]; ok {
		return elem, true
	}

	b.xmap[key] = new

	return new, false
}

// Range iterates over cache records in no particular order.
func (b *Backend[K, V]) Range(f func(key K, r *list.Element[Record[V]]) bool) {
	b.RLock()
	keys := make([]K, 0, len(b.xmap))
	for k := range b.xmap {
		keys = append(keys, k)
	}
	b.RUnlock()

	for _, key := range keys {
		b.RLock()
		elem, ok := b.xmap[key]
		b.RUnlock()
		if ok && !f(key, elem) {
			return
		}
	}
}

// Evict a record.
func (b *Backend[K, V]) Evict(key K) (*list.Element[Record[V]], bool) {
	if elem, ok := b.xmap[key]; ok && elem.Value.Initialized.Load() {
		b.Delete(key)
		b.list.Remove(elem)
		return elem, true
	}

	return nil, false
}

// Delete a record from the backend map.
func (b *Backend[K, V]) Delete(key K) {
	// TODO: realloc map when b.cap == 0 and 2*map_size == map_capacity to avoid memory leak?
	delete(b.xmap, key)
}

// PushBack appends a new record to backend.
func (b *Backend[K, V]) PushBack(elem *list.Element[Record[V]], ttl time.Duration) {
	b.list.PushBack(elem)
	elem.Value.Initialized.Store(true)

	if n := b.overflow(); n > 0 {
		b.startGCOnce()
		b.timer.Reset(0)
	} else if elem.Value.Deadline > 0 {
		b.startGCOnce()
		if b.earliestExpireAt == 0 || elem.Value.Deadline < b.earliestExpireAt {
			b.earliestExpireAt = elem.Value.Deadline
			b.timer.Reset(ttl)
		}
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

func (b *Backend[K, V]) runGC(now int64) {
	b.Lock()
	defer b.Unlock()

	var overflowed map[*list.Element[Record[V]]]bool

	if n := b.overflow(); n > 0 {
		overflowed = make(map[*list.Element[Record[V]]]bool, n)

		elem := b.list.Front()
		overflowed[elem] = true

		for i := 1; i < n; i++ {
			elem = elem.Next()
			overflowed[elem] = true
		}
	}

	var earliest int64

	for key, elem := range b.xmap {
		if elem.Value.Initialized.Load() {
			if len(overflowed) > 0 && overflowed[elem] {
				delete(overflowed, elem)
				b.Delete(key)
				b.list.Remove(elem)
			} else if elem.Value.Deadline > 0 && elem.Value.Deadline < now {
				b.Delete(key)
				b.list.Remove(elem)
			} else if elem.Value.Deadline > 0 && (earliest == 0 || elem.Value.Deadline < earliest) {
				earliest = elem.Value.Deadline
			}
		}
	}

	b.earliestExpireAt = earliest
	if earliest > 0 {
		b.timer.Reset(time.Duration(earliest - now))
	}
}

func (b *Backend[K, V]) overflow() int {
	if b.cap > 0 && b.list.Len() > b.cap {
		return b.list.Len() - b.cap
	}
	return 0
}
