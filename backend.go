package evcache

import (
	"sync"
	"time"

	"github.com/mgnsk/list"
)

type recordList[V any] struct {
	list.ListOf[record[V], *record[V]]
}

// Backend is a cache backend.
type Backend[K comparable, V any] struct {
	timer            *time.Timer
	done             chan struct{}
	xmap             concurrentMap[K, *record[V]]
	list             recordList[V]
	earliestExpireAt int64
	cap              int
	once             sync.Once
	mu               sync.Mutex
}

// NewRWMutexMapBackend creates a read-write mutex map backend.
func NewRWMutexMapBackend[K comparable, V any](capacity int) *Backend[K, V] {
	t := time.NewTimer(0)
	<-t.C

	return &Backend[K, V]{
		timer: t,
		done:  make(chan struct{}),
		xmap:  newRWMutexMap[K, *record[V]](capacity),
		cap:   capacity,
	}
}

// NewSyncMapBackend creates a sync.Map backend.
func NewSyncMapBackend[K comparable, V any](capacity int) *Backend[K, V] {
	t := time.NewTimer(0)
	<-t.C

	return &Backend[K, V]{
		timer: t,
		done:  make(chan struct{}),
		xmap:  newSyncMapWrapper[K, *record[V]](),
		cap:   capacity,
	}
}

func (b *Backend[K, V]) close() error {
	close(b.done)
	return nil
}

func (b *Backend[K, V]) len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.list.Len()
}

func (b *Backend[K, V]) evict(key K) (*record[V], bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if r, ok := b.xmap.Load(key); ok && r.initialized.Load() {
		b.xmap.Delete(key)
		b.list.Remove(r)
		return r, true
	}

	return nil, false
}

func (b *Backend[K, V]) pushBack(r *record[V], ttl time.Duration) {
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

func (b *Backend[K, V]) startGCOnce() {
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

func (b *Backend[K, V]) runGC(now int64) {
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

func (b *Backend[K, V]) overflow() int {
	if b.cap > 0 && b.list.Len() > b.cap {
		return b.list.Len() - b.cap
	}
	return 0
}
