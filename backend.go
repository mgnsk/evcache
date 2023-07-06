package evcache

import (
	"sync"
	"time"

	"github.com/mgnsk/ringlist"
)

type recordList[V any] struct {
	ringlist.List[record[V], *record[V]]
}

type backend[K comparable, V any] struct {
	timer            *time.Timer
	done             chan struct{}
	xmap             syncMapWrapper[K, *record[V]]
	list             recordList[V]
	earliestExpireAt int64
	cap              int
	once             sync.Once
	mu               sync.Mutex
}

func newBackend[K comparable, V any](capacity int) *backend[K, V] {
	t := time.NewTimer(0)
	<-t.C

	return &backend[K, V]{
		timer: t,
		done:  make(chan struct{}),
		cap:   capacity,
	}
}

func (b *backend[K, V]) Close() error {
	close(b.done)
	return nil
}

func (b *backend[K, V]) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.list.Len()
}

func (b *backend[K, V]) Evict(key K) (*record[V], bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if r, ok := b.xmap.Load(key); ok && r.initialized.Load() {
		b.xmap.Delete(key)
		b.list.Remove(r)
		return r, true
	}

	return nil, false
}

func (b *backend[K, V]) PushBack(r *record[V], ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

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
					b.mu.Lock()
					b.runGC(now.UnixNano())
					b.mu.Unlock()
				}
			}
		}()
	})
}

func (b *backend[K, V]) runGC(now int64) {
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
