package evcache

// ListElement is the constraint for a list element.
type ListElement[E any] interface {
	Link(E)
	Unlink()
	Next() E
	Prev() E
}

// RingList is a circular doubly linked list.
// The zero value is a ready to use empty list.
type RingList[T any, E interface {
	*T
	ListElement[E]
}] struct {
	tail E
	len  int
}

// Len returns the number of elements in the list.
func (l RingList[T, E]) Len() int {
	return l.len
}

// Front returns the first element of the list or nil.
func (l *RingList[T, E]) Front() E {
	if l.len == 0 {
		return nil
	}
	return l.tail.Next()
}

// Back returns the last element of the list or nil.
func (l *RingList[T, E]) Back() E {
	return l.tail
}

// PushBack inserts a new element v at the back of the list.
func (l *RingList[T, E]) PushBack(e E) {
	if l.tail != nil {
		l.tail.Link(e)
	}
	l.tail = e
	l.len++
}

// Remove an element from the list.
func (l *RingList[T, E]) Remove(e E) {
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
