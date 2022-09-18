package evcache

import (
	"sync"
	"sync/atomic"
)

type record[V any] struct {
	value       V
	next, prev  *record[V]
	deadline    int64
	wg          sync.WaitGroup
	initialized atomic.Bool
}

func newRecord[V any]() *record[V] {
	r := &record[V]{}
	r.next = r
	r.prev = r
	return r
}

func (r *record[V]) Link(s *record[V]) {
	n := r.next
	r.next = s
	s.prev = r
	n.prev = s
	s.next = n
}

func (r *record[V]) Unlink() {
	r.prev.next = r.next
	r.next.prev = r.prev
	r.next = r
	r.prev = r
}
