package evcache

import (
	"container/ring"
	"sync/atomic"
)

// ringList is a size-limited list using rings.
type ringList struct {
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
	return int(atomic.LoadUint32(&l.size))
}

// PushBack inserts a value at the back of list. If capacity is exceeded,
// an element from the front of list is removed and its value returned.
func (l *ringList) PushBack(value interface{}, r *ring.Ring) (front interface{}) {
	r.Value = value
	if l.back != nil {
		l.back.Link(r)
	}
	l.back = r
	size := atomic.LoadUint32(&l.size)
	if l.capacity > 0 && size+1 > l.capacity {
		front = l.unlink(l.back.Next())
		if front == value {
			panic("evcache: front cannot be value")
		}
		return front
	}
	atomic.AddUint32(&l.size, 1)
	return nil
}

// Pop removes and returns the front element.
func (l *ringList) Pop() (value interface{}) {
	if l.back == nil {
		return nil
	}
	atomic.AddUint32(&l.size, ^uint32(0))
	return l.unlink(l.back.Next())
}

// Remove an element from the list.
func (l *ringList) Remove(r *ring.Ring) (value interface{}) {
	if r.Value == nil {
		// An overflowed element unlinked by PushBack.
		return nil
	}
	atomic.AddUint32(&l.size, ^uint32(0))
	return l.unlink(r)
}

// MoveForward moves element forward by at most delta positions.
func (l *ringList) MoveForward(r *ring.Ring, delta uint32) {
	if r.Value == nil {
		// An overflowed element unlinked by PushBack.
		return
	}
	l.move(r, delta)
}

// Do calls function f on each element of the list, in forward order.
// If f returns false, Do stops the iteration.
// f must not change l.
func (l *ringList) Do(f func(value interface{}) bool) {
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
	r.Prev().Unlink(1)
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
	r.Prev().Unlink(1)
	target.Link(r)
}
