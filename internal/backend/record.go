package backend

import (
	"sync"
)

const (
	stateUninitialized = iota
	stateInitialized
	stateOverwritten
)

// Record is a cache record.
type Record[K comparable, V any] struct {
	Key      K
	Value    V
	deadline int64
	state    int
	wg       sync.WaitGroup
}

// Initialize the record with a value.
func (r *Record[K, V]) Initialize(key K, value V, deadline int64) {
	r.Key = key
	r.Value = value
	r.deadline = deadline
	r.state = stateInitialized
}
