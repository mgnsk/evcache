package evcache

import (
	"container/ring"
	"sync"
	"sync/atomic"
)

type lfuRing struct {
	mu sync.Mutex
	// cursor is the most frequently used record.
	// cursor.Next() is the least frequently used.
	cursor   *ring.Ring
	size     uint32
	capacity uint32
}

func newLFURing(capacity uint32) *lfuRing {
	l := &lfuRing{
		capacity: capacity,
	}
	return l
}

func (l *lfuRing) Len() int {
	return int(atomic.LoadUint32(&l.size))
}

// Push inserts a key as most frequently used. If capacity is exceeded,
// the least frequently used key is returned.
func (l *lfuRing) Push(key interface{}, r *ring.Ring) (lfu interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Value = key
	if l.cursor != nil {
		l.cursor.Link(r)
	}
	l.cursor = r
	size := atomic.AddUint32(&l.size, 1)
	if l.capacity > 0 && size > l.capacity {
		lfuring := l.cursor.Next()
		return lfuring.Value
	}
	return nil
}

// Remove an existing element.
func (l *lfuRing) Remove(r *ring.Ring) (key interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.unlink(r)
}

func (l *lfuRing) Promote(r *ring.Ring, hits uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cursor == nil {
		panic("evcache: cursor must not be nil")
	}
	target := r
	if target == target.Next() {
		return
	}
	for i := uint32(0); i < hits; i++ {
		next := target.Next()
		target = next
		if next == l.cursor {
			break
		}
	}
	if r.Prev().Unlink(1) != r {
		panic("evcache: invalid ring")
	}
	target.Link(r)
	if target == l.cursor {
		l.cursor = r
	}
}

func (l *lfuRing) unlink(r *ring.Ring) (key interface{}) {
	if l.cursor == nil {
		panic("evcache: cursor must not be nil")
	}
	if l.cursor == r {
		l.cursor = l.cursor.Prev()
	}
	if r.Prev().Unlink(1) != r {
		panic("evcache: invalid ring")
	}
	if size := atomic.AddUint32(&l.size, ^uint32(0)); size == 0 {
		l.cursor = nil
	}
	return r.Value
}
