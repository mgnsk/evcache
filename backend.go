package evcache

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

type backend[K comparable, V any] struct {
	timer            *time.Timer
	done             chan struct{}
	xmap             backendMap[K, *list.Element[record[V]]]
	list             list.List[record[V]]
	earliestExpireAt int64
	cap              int
	once             sync.Once
	mu               sync.RWMutex
}

func newBackend[K comparable, V any](capacity int) *backend[K, V] {
	t := time.NewTimer(0)
	<-t.C

	return &backend[K, V]{
		timer: t,
		done:  make(chan struct{}),
		xmap:  make(builtinMap[K, *list.Element[record[V]]], capacity),
		cap:   capacity,
	}
}

func (b *backend[K, V]) Close() error {
	close(b.done)
	return nil
}

func (b *backend[K, V]) LoadOrStore(key K, new *list.Element[record[V]]) (old *list.Element[record[V]], loaded bool) {
	b.mu.RLock()
	if elem, ok := b.xmap.Load(key); ok {
		b.mu.RUnlock()
		return elem, true
	}
	b.mu.RUnlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	if elem, ok := b.xmap.Load(key); ok {
		return elem, true
	}

	b.xmap.Store(key, new)

	return new, false
}

func (b *backend[K, V]) Range(f func(key K, r *list.Element[record[V]]) bool) {
	b.mu.RLock()
	keys := make([]K, 0, b.xmap.Len())
	b.xmap.Range(func(key K, _ *list.Element[record[V]]) bool {
		keys = append(keys, key)
		return true
	})
	b.mu.RUnlock()

	for _, key := range keys {
		b.mu.RLock()
		elem, ok := b.xmap.Load(key)
		b.mu.RUnlock()
		if ok && !f(key, elem) {
			return
		}
	}
}

func (b *backend[K, V]) evict(key K) (*list.Element[record[V]], bool) {
	if elem, ok := b.xmap.Load(key); ok && elem.Value.initialized.Load() {
		b.xmap.Delete(key)
		b.list.Remove(elem)
		return elem, true
	}

	return nil, false
}

func (b *backend[K, V]) pushBack(elem *list.Element[record[V]], ttl time.Duration) {
	b.list.PushBack(elem)
	elem.Value.initialized.Store(true)

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

func (b *backend[K, V]) startGCOnce() {
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

func (b *backend[K, V]) runGC(now int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var overflowed map[*list.Element[record[V]]]bool

	if n := b.overflow(); n > 0 {
		overflowed = make(map[*list.Element[record[V]]]bool, n)

		elem := b.list.Front()
		overflowed[elem] = true

		for i := 1; i < n; i++ {
			elem = elem.Next()
			overflowed[elem] = true
		}
	}

	var earliest int64

	b.xmap.Range(func(key K, elem *list.Element[record[V]]) bool {
		if elem.Value.initialized.Load() {
			if len(overflowed) > 0 && overflowed[elem] {
				delete(overflowed, elem)
				b.xmap.Delete(key)
				b.list.Remove(elem)
			} else if elem.Value.deadline > 0 && elem.Value.deadline < now {
				b.xmap.Delete(key)
				b.list.Remove(elem)
			} else if elem.Value.deadline > 0 && (earliest == 0 || elem.Value.deadline < earliest) {
				earliest = elem.Value.deadline
			}
		}
		return true
	})

	b.earliestExpireAt = earliest
	if earliest > 0 {
		b.timer.Reset(time.Duration(earliest - now))
	}
}

func (b *backend[K, V]) overflow() int {
	if b.cap > 0 && b.list.Len() > b.cap {
		return b.list.Len() - b.cap
	}
	return 0
}
