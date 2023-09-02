package evcache

import (
	"sync"
	"time"

	"github.com/mgnsk/list"
)

type recordList[V any] struct {
	list.ListOf[record[V], *record[V]]
}

type backend[K comparable, V any] struct {
	timer            *time.Timer
	done             chan struct{}
	xmap             backendMap[K, *record[V]]
	list             recordList[V]
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
		xmap:  make(builtinMap[K, *record[V]], capacity),
		cap:   capacity,
	}
}

func (b *backend[K, V]) close() error {
	close(b.done)
	return nil
}

func (b *backend[K, V]) LoadOrStore(key K, new *record[V]) (old *record[V], loaded bool) {
	b.mu.RLock()
	if r, ok := b.xmap.Load(key); ok {
		b.mu.RUnlock()
		return r, true
	}
	b.mu.RUnlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	if r, ok := b.xmap.Load(key); ok {
		return r, true
	}

	b.xmap.Store(key, new)

	return new, false
}

func (b *backend[K, V]) Range(f func(key K, r *record[V]) bool) {
	b.mu.RLock()
	keys := make([]K, 0, b.xmap.Len())
	b.xmap.Range(func(key K, _ *record[V]) bool {
		keys = append(keys, key)
		return true
	})
	b.mu.RUnlock()

	for _, key := range keys {
		b.mu.RLock()
		r, ok := b.xmap.Load(key)
		b.mu.RUnlock()
		if ok && !f(key, r) {
			return
		}
	}
}

func (b *backend[K, V]) evict(key K) (*record[V], bool) {
	if r, ok := b.xmap.Load(key); ok && r.initialized.Load() {
		b.xmap.Delete(key)
		b.list.Remove(r)
		return r, true
	}

	return nil, false
}

func (b *backend[K, V]) pushBack(r *record[V], ttl time.Duration) {
	b.list.PushBack(r)
	r.initialized.Store(true)

	if n := b.overflow(); n > 0 {
		b.startGCOnce()
		b.timer.Reset(0)
	} else if r.deadline > 0 {
		b.startGCOnce()
		if b.earliestExpireAt == 0 || r.deadline < b.earliestExpireAt {
			b.earliestExpireAt = r.deadline
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

	var overflowed map[*record[V]]bool

	if n := b.overflow(); n > 0 {
		overflowed = make(map[*record[V]]bool, n)

		r := b.list.Front()
		overflowed[r] = true

		for i := 1; i < n; i++ {
			r = r.Next()
			overflowed[r] = true
		}
	}

	var earliest int64

	b.xmap.Range(func(key K, r *record[V]) bool {
		if r.initialized.Load() {
			if len(overflowed) > 0 && overflowed[r] || r.deadline > 0 && r.deadline < now {
				b.xmap.Delete(key)
				b.list.Remove(r)
			} else if r.deadline > 0 && (earliest == 0 || r.deadline < earliest) {
				earliest = r.deadline
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
