package evcache

import (
	"sync"
	"time"
)

type backend[K comparable, V any] struct {
	tail  *record[V]
	timer *time.Timer
	done  chan struct{}
	sync.Map
	earliestExpireAt int64
	cap              int
	len              int
	once             sync.Once
	mu               sync.Mutex
}

func newBackend[K comparable, V any](capacity int) *backend[K, V] {
	t := time.NewTimer(0)
	<-t.C

	return &backend[K, V]{
		done:  make(chan struct{}),
		timer: t,
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

	return b.len
}

func (b *backend[K, V]) Evict(key K) (*record[V], bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if rec, ok := b.Load(key); ok {
		if r := rec.(*record[V]); r.initialized.Load() {
			b.Delete(key)
			b.unlink(r)

			return r, true
		}
	}

	return nil, false
}

func (b *backend[K, V]) PushBack(r *record[V], ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.tail != nil {
		b.tail.Link(r)
	}

	b.tail = r
	b.len++
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

		r := b.tail.next // front element
		overflowed[r] = true

		for i := 1; i < n; i++ {
			r = r.next
			overflowed[r] = true
		}
	}

	var earliest int64

	b.Range(func(key, value any) bool {
		if r := value.(*record[V]); r.initialized.Load() {
			if len(overflowed) > 0 && overflowed[r] || r.deadline > 0 && r.deadline < now {
				b.Delete(key.(K))
				b.unlink(r)
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
	if b.cap > 0 && b.len > b.cap {
		return b.len - b.cap
	}
	return 0
}

func (b *backend[K, V]) unlink(r *record[V]) {
	if r == b.tail {
		if b.len == 1 {
			b.tail = nil
		} else {
			b.tail = r.prev
		}
	}

	r.Unlink()
	b.len--
}
