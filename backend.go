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
	earliestGCAt int64
	cap          int
	len          int
	once         sync.Once
	mu           sync.Mutex
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

	return b.doEvict(key)
}

func (b *backend[K, V]) PushBack(r *record[V], ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.tail != nil {
		b.tail.Link(r)
	}

	b.tail = r
	b.len++

	if n := b.overflow(); n > 0 {
		b.runOnce()
		b.resetTimer(0, 0)
	} else if r.deadline > 0 {
		b.runOnce()
		b.resetTimer(r.deadline, r.deadline-ttl.Nanoseconds())
	}
}

func (b *backend[K, V]) runOnce() {
	b.once.Do(func() {
		go func() {
			for {
				select {
				case <-b.done:
					return
				case now := <-b.timer.C:
					func() {
						b.mu.Lock()
						defer b.mu.Unlock()
						b.runGC(now.UnixNano())
					}()
				}
			}
		}()
	})
}

func (b *backend[K, V]) resetTimer(deadline, now int64) {
	if deadline == 0 || deadline < now {
		b.timer.Reset(0)
	} else if b.earliestGCAt == 0 || deadline < b.earliestGCAt {
		b.earliestGCAt = deadline
		b.timer.Reset(time.Duration(deadline - now))
	}
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
			if b.cap > 0 && overflowed[r] || r.deadline > 0 && r.deadline < now {
				b.doEvict(key.(K))
			}

			if earliest == 0 && r.deadline > 0 {
				earliest = r.deadline
			} else if r.deadline < earliest {
				earliest = r.deadline
			}
		}

		return true
	})

	if earliest > 0 {
		// Reset the timer only when there is a known TTL record.
		b.resetTimer(earliest, now)
	}
}

func (b *backend[K, V]) overflow() int {
	if b.cap > 0 && b.len > b.cap {
		return b.len - b.cap
	}
	return 0
}

func (b *backend[K, V]) doEvict(key K) (*record[V], bool) {
	if rec, ok := b.Load(key); ok {
		if r := rec.(*record[V]); r.initialized.Load() {
			b.Delete(key)

			if r == b.tail {
				if b.len == 1 {
					b.tail = nil
				} else {
					b.tail = r.prev
				}
			}

			r.Unlink()
			b.len--

			return r, true
		}
	}

	return nil, false
}
