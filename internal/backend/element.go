package backend

import (
	"sync"
	"sync/atomic"
)

// Element is a cache element.
type Element[V any] struct {
	Value       V
	deadline    int64
	epoch       atomic.Uint64
	wg          sync.WaitGroup
	initialized bool
}

// Wait for the record to be initialized or discarded.
func (e *Element[V]) Wait() {
	e.wg.Wait()
}
