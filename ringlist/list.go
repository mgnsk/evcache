package ringlist

import (
	"container/ring"
)

// List is a list with capacity and overflow cleanup.
type List struct {
	tail *ring.Ring
	len  int
	cap  int
}

// New creates an empty list with capacity cap.
// When cap == 0, the list has infinite capacity.
func New(cap int) *List {
	if cap < 0 {
		return nil
	}

	return &List{cap: cap}
}

// Len returns the number of elements in list l.
func (l *List) Len() int {
	return l.len
}

// Back returns the last element of list l or nil if the list is empty.
func (l *List) Back() *ring.Ring {
	return l.tail
}

// Front returns the first element of list l or nil if the list is empty.
func (l *List) Front() *ring.Ring {
	if l.tail == nil {
		return nil
	}
	return l.tail.Next()
}

// PushBack inserts an element at the back of list l. If capacity is exceeded,
// the front element is removed and its value returned.
func (l *List) PushBack(r *ring.Ring) (front interface{}) {
	if l.tail != nil {
		l.tail.Link(r)
	}

	l.tail = r
	l.len++

	if l.cap > 0 && l.len > l.cap {
		return l.Remove(l.Front())
	}

	return nil
}

// PushBack inserts an element at the front of list l. If capacity is exceeded,
// the back element is removed and its value returned.
func (l *List) PushFront(r *ring.Ring) (back interface{}) {
	if l.tail != nil {
		l.tail.Link(r)
	} else {
		l.tail = r
	}

	l.len++

	if l.cap > 0 && l.len > l.cap {
		return l.Remove(l.Back())
	}

	return nil

}

// Do calls function f on each element of the list, in forward order.
// If f returns false, Do stops the iteration.
// f must not change l.
func (l *List) Do(f func(value interface{}) bool) {
	r := l.Front()
	if r == nil {
		return
	}

	if !f(r.Value) {
		return
	}

	for p := r.Next(); p != r; p = p.Next() {
		if !f(p.Value) {
			return
		}
	}
}

// Remove an element from the list.
func (l *List) Remove(r *ring.Ring) (value interface{}) {
	l.unlink(r)

	value = r.Value
	r.Value = nil

	return value
}

// MoveToFront moves the element to the front of list l.
func (l *List) MoveToFront(r *ring.Ring) {
	l.MoveBefore(r, l.Front())
}

// MoveToBack moves the element to the back of list l.
func (l *List) MoveToBack(r *ring.Ring) {
	l.MoveAfter(r, l.Back())
}

// Move moves element r forward or backwards by at most delta positions
// or until the element becomes the front or back element in the list.
func (l *List) Move(r *ring.Ring, delta int) {
	if l.tail == nil {
		panic("ringlist: invalid element")
	}

	if r == r.Next() {
		if r != l.tail {
			panic("ringlist: invalid element")
		}
		return
	}

	mark := r

	switch {
	case delta == 0:
		return

	case delta > 0:
		for i := 0; i < delta; i++ {
			if mark = mark.Next(); mark == l.tail {
				break
			}
		}

		l.MoveAfter(r, mark)

	case delta < 0:
		for i := 0; i > delta; i-- {
			if mark = mark.Prev(); mark == l.tail.Next() {
				break
			}
		}

		l.MoveBefore(r, mark)
	}
}

// MoveAfter moves an element to its new position after mark.
func (l *List) MoveAfter(r, mark *ring.Ring) {
	l.unlink(r)

	mark.Link(r)
	l.len++

	if mark == l.tail {
		l.tail = r
	}
}

// MoveAfter moves an element to its new position before mark.
func (l *List) MoveBefore(r, mark *ring.Ring) {
	l.unlink(r)

	mark.Prev().Link(r)

	l.len++
}

func (l *List) unlink(r *ring.Ring) {
	if l.tail == nil {
		panic("ringlist: invalid element")
	}

	if r == r.Next() {
		if r != l.tail {
			panic("ringlist: invalid element")
		}
		// Remove the only element.
		l.tail = nil
	} else if r == l.tail {
		// Remove the last element.
		l.tail = r.Prev()
	}

	r.Prev().Unlink(1)
	l.len--
}
