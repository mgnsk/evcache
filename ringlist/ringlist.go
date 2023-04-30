package ringlist

// ElementList is a built in list type that uses the Element type as its element.
// The zero value is a ready to use empty list.
type ElementList[V any] struct {
	List[Element[V], *Element[V]]
}

// ListElement is the constraint for a generic list element.
type ListElement[E any] interface {
	Link(E)
	Unlink()
	Next() E
	Prev() E
}

// List is a generic circular doubly linked list.
// The zero value is a ready to use empty list.
type List[T any, E interface {
	*T
	ListElement[E]
}] struct {
	tail E
	len  int
}

// Len returns the number of elements in the list.
func (l *List[T, E]) Len() int {
	return l.len
}

// Front returns the first element of the list or nil.
func (l *List[T, E]) Front() E {
	if l.len == 0 {
		return nil
	}
	return l.tail.Next()
}

// Back returns the last element of the list or nil.
func (l *List[T, E]) Back() E {
	return l.tail
}

// PushBack inserts a new element at the back of list l.
func (l *List[T, E]) PushBack(e E) {
	if l.tail != nil {
		l.tail.Link(e)
	}
	l.tail = e
	l.len++
}

// PushFront inserts a new element at the front of list l.
func (l *List[T, E]) PushFront(e E) {
	if l.tail != nil {
		l.tail.Link(e)
	} else {
		l.tail = e
	}
	l.len++
}

// Do calls function f on each element of the list, in forward order.
// If f returns false, Do stops the iteration.
// f must not change l.
func (l *List[T, E]) Do(f func(e E) bool) {
	e := l.Front()
	if e == nil {
		return
	}

	if !f(e) {
		return
	}

	for p := e.Next(); p != e; p = p.Next() {
		if !f(p) {
			return
		}
	}
}

// MoveAfter moves an element to its new position after mark.
func (l *List[T, E]) MoveAfter(e, mark E) {
	l.Remove(e)

	mark.Link(e)
	l.len++

	if mark == l.tail {
		l.tail = e
	}
}

// MoveBefore moves an element to its new position before mark.
func (l *List[T, E]) MoveBefore(e, mark E) {
	l.Remove(e)

	mark.Prev().Link(e)

	l.len++
}

// MoveToFront moves the element to the front of list l.
func (l *List[T, E]) MoveToFront(e E) {
	l.MoveBefore(e, l.Front())
}

// MoveToBack moves the element to the back of list l.
func (l *List[T, E]) MoveToBack(e E) {
	l.MoveAfter(e, l.Back())
}

// Move moves element e forward or backwards by at most delta positions
// or until the element becomes the front or back element in the list.
func (l *List[T, E]) Move(e E, delta int) {
	if l.tail == nil {
		panic("ringlist: invalid element")
	}

	if l.len == 1 && e != l.tail {
		panic("ringlist: invalid element")
	}

	mark := e

	switch {
	case delta == 0:
		return

	case delta > 0:
		for i := 0; i < delta; i++ {
			if mark = mark.Next(); mark == l.tail {
				break
			}
		}

		l.MoveAfter(e, mark)

	case delta < 0:
		for i := 0; i > delta; i-- {
			if mark = mark.Prev(); mark == l.tail.Next() {
				break
			}
		}

		l.MoveBefore(e, mark)
	}
}

// Remove an element from the list.
func (l *List[T, E]) Remove(e E) {
	if e == l.tail {
		if l.len == 1 {
			l.tail = nil
		} else {
			l.tail = e.Prev()
		}
	}
	e.Unlink()
	l.len--
}
