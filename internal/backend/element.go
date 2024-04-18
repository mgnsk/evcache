package backend

import (
	"sync"
)

// Record is a cache record.
type Record[K comparable, V any] struct {
	Key         K
	Value       V
	deadline    int64
	initialized bool
	wg          sync.WaitGroup
}
