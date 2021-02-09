package evcache

import (
	"container/ring"
	"sync"
	"sync/atomic"
	"time"
)

const (
	stateInactive uint32 = iota
	stateActive
)

type record struct {
	mu   sync.RWMutex
	wg   sync.WaitGroup
	ring *ring.Ring

	value   interface{}
	expires int64
	hits    uint32
	state   uint32
}

func (r *record) Close() error {
	r.wg.Done()
	return nil
}

func (r *record) IsActive() bool {
	return atomic.LoadUint32(&r.state) == stateActive
}

func (r *record) Load() (interface{}, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.IsActive() {
		return nil, false
	}
	r.wg.Add(1)
	atomic.AddUint32(&r.hits, 1)
	return r.value, true
}

func (r *record) LoadAndDelete() (interface{}, bool) {
	if !atomic.CompareAndSwapUint32(&r.state, stateActive, stateInactive) {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value := r.value
	r.value = nil
	r.expires = 0
	r.hits = 0
	return value, true
}

func (r *record) init(value interface{}, ttl time.Duration) {
	if !atomic.CompareAndSwapUint32(&r.state, stateInactive, stateActive) {
		panic("evcache: invalid record state")
	}
	r.value = value
	if ttl > 0 {
		r.expires = time.Now().Add(ttl).UnixNano()
	}
	r.hits = 1
	r.wg.Add(1)
}
