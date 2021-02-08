package evcache

import (
	"container/ring"
	"sync"
	"sync/atomic"
)

const (
	stateZombie = iota
	stateAlive
	stateDeleted
)

type record struct {
	mu    sync.RWMutex
	value interface{}
	ring  *ring.Ring
	// value atomic.Value
	wg      sync.WaitGroup
	state   uint32
	hits    uint32
	expires int64
}

func (r *record) Close() error {
	r.wg.Done()
	return nil
}

func (r *record) hit() {
	r.wg.Add(1)
	atomic.AddUint32(&r.hits, 1)
}

func (r *record) Load() (interface{}, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.valid() {
		return nil, false
	}
	r.hit()
	return r.value, true
}

func (r *record) valid() bool {
	return atomic.LoadUint32(&r.state) == stateAlive
}

func (r *record) softDelete() bool {
	return atomic.CompareAndSwapUint32(&r.state, stateAlive, stateDeleted)
}

//func (r *record) finalize() bool {
//return atomic.CompareAndSwapUint32(&r.state, stateDeleted, stateZombie)
//}

func (r *record) reset() {
	r.value = nil
	// r.once = sync.Once{}
	r.wg = sync.WaitGroup{}
	// r.state = 0
	atomic.StoreUint32(&r.state, stateZombie)
	r.hits = 0
	r.expires = 0
}
