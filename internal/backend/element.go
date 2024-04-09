package backend

import (
	"sync"
)

// Record is a cache record.
type Record[V any] struct {
	Value       V
	deadline    int64
	initialized bool
	wg          sync.WaitGroup
}
