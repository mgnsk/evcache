/*
Package backend implements cache backend.
*/
package backend

import (
	"maps"
	"sync"
	"time"

	"github.com/mgnsk/ringlist"
)

// Backend implements cache backend.
type Backend[K comparable, V any] struct {
	Policy           Policy
	timer            *time.Timer
	done             chan struct{}
	xmap             map[K]*ringlist.Element[Record[K, V]] // map of uninitialized and initialized elements
	list             ringlist.List[Record[K, V]]           // list of initialized elements
	pool             sync.Pool                             // pool of elements
	earliestExpireAt int64
	cap              int
	lastLen          int
	numDeleted       uint64
	gcStarted        bool
	mu               sync.Mutex
}

// NewBackend creates a new cache backend.
func NewBackend[K comparable, V any](capacity int) *Backend[K, V] {
	t := time.NewTimer(0)
	<-t.C

	return &Backend[K, V]{
		timer: t,
		done:  make(chan struct{}),
		xmap:  make(map[K]*ringlist.Element[Record[K, V]], capacity),
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
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.list.Len()
}

// Reserve a new uninitialized element.
func (b *Backend[K, V]) Reserve(key K) *ringlist.Element[Record[K, V]] {
	elem, ok := b.pool.Get().(*ringlist.Element[Record[K, V]])
	if !ok {
		elem = ringlist.NewElement(Record[K, V]{})
	}

	elem.Value.Key = key
	elem.Value.wg.Add(1)

	return elem
}

// Release a reserved uninitialized element.
func (b *Backend[K, V]) Release(elem *ringlist.Element[Record[K, V]]) {
	elem.Value.Key = *new(K)
	elem.Value.wg.Done()
	b.pool.Put(elem)
}

// Discard a reserved uninitialized element.
func (b *Backend[K, V]) Discard(elem *ringlist.Element[Record[K, V]]) {
	b.mu.Lock()
	delete(b.xmap, elem.Value.Key)
	elem.Value.wg.Done()
	b.mu.Unlock()
}

// Initialize a previously stored uninitialized element.
func (b *Backend[K, V]) Initialize(elem *ringlist.Element[Record[K, V]], value V, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	defer elem.Value.wg.Done()

	elem.Value.Value = value
	if ttl > 0 {
		elem.Value.deadline = time.Now().Add(ttl).UnixNano()
	}

	elem.Value.initialized = true
	b.list.PushBack(elem)

	if n := b.overflow(); n > 0 {
		b.delete(b.list.Front())
	}

	if elem.Value.deadline > 0 {
		b.startGCOnce()
		if b.earliestExpireAt == 0 || elem.Value.deadline < b.earliestExpireAt {
			b.earliestExpireAt = elem.Value.deadline
			b.timer.Reset(ttl)
		}
	}
}

func (b *Backend[K, V]) hit(elem *ringlist.Element[Record[K, V]]) {
	switch b.Policy {
	case Default:
	case LFU:
		b.list.MoveAfter(elem, elem.Next())
	case LRU:
		b.list.MoveAfter(elem, b.list.Back())
	}
}

// Load an initialized element.
func (b *Backend[K, V]) Load(key K) (value *ringlist.Element[Record[K, V]], ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if elem, ok := b.xmap[key]; ok && elem.Value.initialized {
		b.hit(elem)
		return elem, true
	}

	return nil, false
}

// LoadOrStore loads or stores an element.
func (b *Backend[K, V]) LoadOrStore(new *ringlist.Element[Record[K, V]]) (old *ringlist.Element[Record[K, V]], loaded bool) {
tryLoadStore:
	b.mu.Lock()
	if elem, ok := b.xmap[new.Value.Key]; ok {
		if elem.Value.initialized {
			b.hit(elem)
			b.mu.Unlock()
			return elem, true
		}

		b.mu.Unlock()
		elem.Value.wg.Wait()
		goto tryLoadStore
	}

	b.xmap[new.Value.Key] = new

	b.mu.Unlock()

	return new, false
}

// Range iterates over initialized cache elements in no particular order or consistency.
func (b *Backend[K, V]) Range(f func(r *ringlist.Element[Record[K, V]]) bool) {
	b.mu.Lock()
	keys := make([]K, 0, len(b.xmap))
	for key := range b.xmap {
		keys = append(keys, key)
	}
	b.mu.Unlock()

	for _, key := range keys {
		b.mu.Lock()
		elem, ok := b.xmap[key]
		initialized := ok && elem.Value.initialized
		b.mu.Unlock()
		if initialized && !f(elem) {
			return
		}
	}
}

// Evict an element.
func (b *Backend[K, V]) Evict(key K) (V, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var zero V

	if elem, ok := b.xmap[key]; ok && elem.Value.initialized {
		b.delete(elem)
		return elem.Value.Value, true
	}

	return zero, false
}

// overflow returns the number of overflowed elements.
func (b *Backend[K, V]) overflow() int {
	if b.cap > 0 && b.list.Len() > b.cap {
		return b.list.Len() - b.cap
	}
	return 0
}

func (b *Backend[K, V]) delete(elem *ringlist.Element[Record[K, V]]) {
	delete(b.xmap, elem.Value.Key)
	b.list.Remove(elem)
	b.numDeleted++

	if b.lastLen == 0 {
		b.lastLen = b.list.Len()
	}

	if b.numDeleted > uint64(b.lastLen)/2 {
		b.numDeleted = 0
		b.lastLen = b.list.Len()
		b.xmap = maps.Clone(b.xmap)
	}
}

func (b *Backend[K, V]) startGCOnce() {
	if b.gcStarted {
		return
	}

	b.gcStarted = true

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
}

func (b *Backend[K, V]) RunGC(now int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var (
		earliest int64
		deleted  []*ringlist.Element[Record[K, V]]
	)

	b.list.Do(func(elem *ringlist.Element[Record[K, V]]) bool {
		deadline := elem.Value.deadline

		if deadline > 0 && deadline < now {
			deleted = append(deleted, elem)
			return true
		}

		if deadline > 0 && (earliest == 0 || deadline < earliest) {
			earliest = deadline
		}

		return true
	})

	for _, elem := range deleted {
		b.delete(elem)
	}

	b.earliestExpireAt = earliest
	if earliest > 0 {
		b.timer.Reset(time.Duration(earliest - now))
	}
}
