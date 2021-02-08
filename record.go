package evcache

import (
	"container/ring"
	"sync"
	"sync/atomic"
	"time"
)

const (
	stateZombie = iota
	stateAlive
	stateDeleted
)

type record struct {
	mu   sync.RWMutex
	wg   sync.WaitGroup
	ring *ring.Ring

	value   interface{}
	state   uint32
	hits    uint32
	expires int64
}

func (r *record) Close() error {
	r.wg.Done()
	return nil
}

func (r *record) Load() (interface{}, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.valid() {
		return nil, false
	}
	r.wg.Add(1)
	atomic.AddUint32(&r.hits, 1)
	return r.value, true
}

func (r *record) init(value interface{}, ttl time.Duration) {
	r.value = value
	if ttl > 0 {
		r.expires = time.Now().Add(ttl).UnixNano()
	}
	r.state = stateAlive
	r.wg.Add(1)
	atomic.AddUint32(&r.hits, 1)
}

func (r *record) finish() {
	r.value = nil
	r.state = stateZombie
	r.hits = 0
	r.expires = 0
}

func (r *record) valid() bool {
	return atomic.LoadUint32(&r.state) == stateAlive
}

func (r *record) softDelete() bool {
	return atomic.CompareAndSwapUint32(&r.state, stateAlive, stateDeleted)
}
