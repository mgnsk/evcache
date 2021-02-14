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

func newRecord() *record {
	return &record{ring: ring.New(1)}
}

func (r *record) Close() error {
	r.wg.Done()
	return nil
}

func (r *record) Active() bool {
	return atomic.LoadUint32(&r.state) == stateActive
}

func (r *record) Load() (interface{}, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.Active() {
		return nil, false
	}
	return r.value, true
}

func (r *record) LoadAndHit() (interface{}, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.Active() {
		return nil, false
	}
	atomic.AddUint32(&r.hits, 1)
	r.wg.Add(1)
	return r.value, true
}

func (r *record) LoadAndReset() (interface{}, bool) {
	if !atomic.CompareAndSwapUint32(&r.state, stateActive, stateInactive) {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value := r.value
	r.value = nil
	atomic.StoreInt64(&r.expires, 0)
	atomic.StoreUint32(&r.hits, 0)
	return value, true
}

func (r *record) init(value interface{}, ttl time.Duration) {
	if !atomic.CompareAndSwapUint32(&r.state, stateInactive, stateActive) {
		panic("evcache: invalid record state")
	}
	r.value = value
	if ttl > 0 {
		atomic.StoreInt64(&r.expires, time.Now().Add(ttl).UnixNano())
	}
}

func (r *record) expired(now int64) bool {
	expires := atomic.LoadInt64(&r.expires)
	return expires > 0 && expires < now
}
