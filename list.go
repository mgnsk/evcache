package evcache

import (
	"container/ring"
	"sync"
)

// ringList is a size-limited list using rings.
type ringList struct {
	mu       sync.RWMutex
	back     *ring.Ring
	size     uint32
	capacity uint32
}

// newRingList creates an empty list.
func newRingList(capacity uint32) *ringList {
	l := &ringList{
		capacity: capacity,
	}
	return l
}

// Len returns the length of elements in the ring.
func (l *ringList) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return int(l.size)
}

// Pop removes and returns the front element.
func (l *ringList) Pop() (value interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.back == nil {
		return nil
	}
	return l.unlink(l.back.Next())
}

// PushBack inserts a value at the back of list. If capacity is exceeded,
// an element from the front of list is removed and its value returned.
func (l *ringList) PushBack(value interface{}, r *ring.Ring) (front interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Value = value
	l.link(r)
	if l.capacity > 0 && l.size > l.capacity {
		front = l.unlink(l.back.Next())
		if front == value {
			panic("evcache: front cannot be value")
		}
		return front
	}
	return nil
}

// Remove an element from the list.
func (l *ringList) Remove(r *ring.Ring) (value interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.Value == nil {
		return nil
	}
	return l.unlink(r)
}

// MoveForward moves element forward by at most delta positions.
func (l *ringList) MoveForward(r *ring.Ring, delta uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.Value == nil {
		// An overflowed ring which was already unlinked by l.Push
		// but the record not yet evicted.
		return
	}
	l.move(r, delta)
}

// Do calls function f on each element of the list, in forward order.
// If f returns false, Do stops the iteration.
// f must not change l.
func (l *ringList) Do(f func(value interface{}) bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.back == nil {
		return
	}
	r := l.back.Next()
	if !f(r.Value) {
		return
	}
	for p := r.Next(); p != r; p = p.Next() {
		if !f(p.Value) {
			return
		}
	}
}

func (l *ringList) link(r *ring.Ring) {
	if l.back != nil {
		l.back.Link(r)
	}
	l.back = r
	l.size++
}

func (l *ringList) unlink(r *ring.Ring) (key interface{}) {
	if l.back == nil {
		panic("evcache: invalid cursor")
	}
	if r == r.Next() {
		if r != l.back {
			panic("evcache: invalid ring")
		}
		l.back = nil
	} else if r == l.back {
		l.back = r.Prev()
	}
	if r.Prev().Unlink(1) != r {
		panic("evcache: invalid ring")
	}
	l.size--
	key = r.Value
	r.Value = nil
	return key
}

func (l *ringList) move(r *ring.Ring, delta uint32) {
	if l.back == nil {
		panic("evcache: invalid cursor")
	}
	if r == r.Next() || r == l.back {
		return
	}
	target := r
	for i := uint32(0); i < delta; i++ {
		if target = target.Next(); target == l.back {
			l.back = r
			break
		}
	}
	if r.Prev().Unlink(1) != r {
		panic("evcache: invalid ring")
	}
	target.Link(r)
}
