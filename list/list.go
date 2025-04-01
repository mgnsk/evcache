package list

// List is a doubly linked list.
//
// The zero value is a ready to use empty list.
type List[V any] struct {
	tail *Element[V]
	len  int
}

// Len returns the number of elements in the list.
func (l *List[V]) Len() int {
	return l.len
}

// Front returns the first element of the list or nil.
func (l *List[V]) Front() *Element[V] {
	if l.len == 0 {
		return nil
	}
	return l.tail.next
}

// Back returns the last element of the list or nil.
func (l *List[V]) Back() *Element[V] {
	return l.tail
}

// PushBack inserts a value at the back of list l and returns the new element.
func (l *List[V]) PushBack(value V) *Element[V] {
	e := NewElement(value)
	l.PushBackElem(e)
	return e
}

// PushBackElem inserts a new element at the back of list l.
func (l *List[V]) PushBackElem(e *Element[V]) {
	e.list = l
	if l.tail != nil {
		l.tail.link(e)
	}
	l.tail = e
	l.len++
}

// PushFront inserts a value at the front of list l and returns the new element.
func (l *List[V]) PushFront(value V) *Element[V] {
	e := NewElement(value)
	l.PushFrontElem(e)
	return e
}

// PushFrontElem inserts a new element at the front of list l.
func (l *List[V]) PushFrontElem(e *Element[V]) {
	e.list = l
	if l.tail != nil {
		l.tail.link(e)
	} else {
		l.tail = e
	}
	l.len++
}

// Do calls function f on each element of the list, in forward order.
// If f returns false, Do stops the iteration.
// f must not change l.
func (l *List[V]) Do(f func(e *Element[V]) bool) {
	e := l.Front()
	if e == nil {
		return
	}

	if !f(e) {
		return
	}

	for p := e.next; p != e; p = p.next {
		if !f(p) {
			return
		}
	}
}

// MoveAfter moves an element to its new position after mark.
// If mark == l.Back(), e becomes the new back element.
func (l *List[V]) MoveAfter(e, mark *Element[V]) {
	if e == mark {
		return
	}

	l.Remove(e)

	mark.link(e)
	l.len++

	if mark == l.tail {
		l.tail = e
	}
}

// MoveBefore moves an element to its new position before mark.
// if mark == l.Front(), e becomes the new front element.
func (l *List[V]) MoveBefore(e, mark *Element[V]) {
	if e == mark {
		return
	}

	l.Remove(e)

	mark.prev.link(e)

	l.len++
}

// MoveToFront moves the element to the front of list l.
func (l *List[V]) MoveToFront(e *Element[V]) {
	l.MoveBefore(e, l.Front())
}

// MoveToBack moves the element to the back of list l.
func (l *List[V]) MoveToBack(e *Element[V]) {
	l.MoveAfter(e, l.Back())
}

// Move moves element e forward or backwards by at most delta positions
// or until the element becomes the front or back element in the list.
func (l *List[V]) Move(e *Element[V], delta int) {
	if l.tail == nil {
		panic("list: invalid element")
	}

	if l.len == 1 && e != l.tail {
		panic("list: invalid element")
	}

	mark := e

	switch {
	case delta == 0:
		return

	case delta > 0:
		for i := 0; i < delta; i++ {
			if mark = mark.next; mark == l.tail {
				break
			}
		}

		l.MoveAfter(e, mark)

	case delta < 0:
		for i := 0; i > delta; i-- {
			if mark = mark.prev; mark == l.tail.next {
				break
			}
		}

		l.MoveBefore(e, mark)
	}
}

// Remove an element from the list.
func (l *List[V]) Remove(e *Element[V]) {
	if e == l.tail {
		if l.len == 1 {
			l.tail = nil
		} else {
			l.tail = e.prev
		}
	}
	e.unlink()
	l.len--
}
