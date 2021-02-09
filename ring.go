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
	l.link(r)
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
	l.move(r, hits)
}

func (l *lfuRing) link(r *ring.Ring) {
	if l.cursor != nil {
		l.cursor.Link(r)
	}
	l.cursor = r
	l.size++
}

func (l *lfuRing) unlink(r *ring.Ring) (key interface{}) {
	if l.cursor == nil {
		panic("evcache: invalid cursor")
	}
	if r == r.Next() {
		if r != l.cursor {
			panic("evcache: invalid ring")
		}
		l.cursor = nil
	} else if r == l.cursor {
		l.cursor = r.Prev()
	}
	if r.Prev().Unlink(1) != r {
		panic("evcache: invalid ring")
	}
	l.size--
	key = r.Value
	r.Value = nil
	return key
}

func (l *lfuRing) move(r *ring.Ring, hits uint32) {
	if l.cursor == nil {
		panic("evcache: invalid cursor")
	}
	if r == r.Next() || r == l.cursor {
		return
	}
	target := r
	for i := uint32(0); i < hits; i++ {
		if target = target.Next(); target == l.cursor {
			l.cursor = r
			break
		}
	}
	if r.Prev().Unlink(1) != r {
		panic("evcache: invalid ring")
	}
	target.Link(r)
}
