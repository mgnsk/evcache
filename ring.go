package evcache

import (
	"container/ring"
	"sync"
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
	l.mu.Lock()
	defer l.mu.Unlock()
	return int(l.size)
}

// Push inserts a key as most frequently used. If capacity is exceeded,
// the least frequently used element is removed and its key returned.
func (l *lfuRing) Push(key interface{}, r *ring.Ring) (lfuKey interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Value = key
	if l.cursor != nil {
		l.cursor.Link(r)
	}
	l.cursor = r
	l.size++
	if l.capacity > 0 && l.size > l.capacity {
		lfuKey = l.unlink(l.cursor.Next())
		if lfuKey == key {
			panic("evcache: lfuKey cannot be key")
		}
		return lfuKey
	}
	return nil
}

// Remove an element.
func (l *lfuRing) Remove(r *ring.Ring) (key interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.Value == nil {
		// An overflowed ring which was already unlinked by l.Push
		// but the record not yet evicted.
		return nil
	}
	return l.unlink(r)
}

// Promote moves element towards cursor by at most hits positions.
func (l *lfuRing) Promote(r *ring.Ring, hits uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.Value == nil {
		// An overflowed ring which was already unlinked by l.Push
		// but the record not yet evicted.
		return
	}
	if l.cursor == nil {
		panic("evcache: cursor must not be nil")
	}
	if r == r.Next() {
		return
	}
	target := r
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
	if r.Value == nil {
		panic("evcache: r.Value must not be nil")
	}
	if l.cursor == nil {
		panic("evcache: cursor must not be nil")
	}
	if l.cursor == r {
		l.cursor = l.cursor.Prev()
	}
	if r.Prev().Unlink(1) != r {
		panic("evcache: invalid ring")
	}
	l.size--
	if l.size == 0 {
		l.cursor = nil
	}
	key = r.Value
	r.Value = nil
	return key
}
