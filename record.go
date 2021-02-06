package evcache

import (
	"sync"
	"sync/atomic"
)

const (
	stateAlive = iota
	stateDeleted
)

type record struct {
	mu      sync.RWMutex
	value   interface{}
	once    sync.Once
	wg      sync.WaitGroup
	state   uint32
	hits    uint32
	expires int64
}

func (r *record) Close() error {
	r.wg.Done()
	return nil
}

func (r *record) load() interface{} {
	r.wg.Add(1)
	atomic.AddUint32(&r.hits, 1)
	return r.value
}

func (r *record) valid() bool {
	return atomic.LoadUint32(&r.state) == stateAlive
}

func (r *record) softDelete() bool {
	return atomic.CompareAndSwapUint32(&r.state, stateAlive, stateDeleted)
}
