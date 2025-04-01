package list

// Element is a list element.
type Element[V any] struct {
	next, prev *Element[V]
	list       *List[V]
	Value      V
}

// Next returns the next element or nil if e is the last element in its list.
func (e *Element[V]) Next() *Element[V] {
	if e != e.next && e.list != nil && e != e.list.tail {
		return e.next
	}
	return nil
}

// Prev returns the previous element or nil if e is the first element in its list.
func (e *Element[V]) Prev() *Element[V] {
	if e != e.prev && e.list != nil && e.prev != e.list.tail {
		return e.prev
	}
	return nil
}

// link inserts an element after this element.
func (e *Element[V]) link(s *Element[V]) {
	n := e.next
	e.next = s
	s.prev = e
	n.prev = s
	s.next = n
}

// unlink unlinks this element.
func (e *Element[V]) unlink() {
	e.prev.next = e.next
	e.next.prev = e.prev
	e.next = e
	e.prev = e
}
