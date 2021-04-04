package evcache

import (
	"container/ring"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// The default state represents a record being fetched.
	_ uint32 = iota
	stateActive
	stateEvicting
)

type record struct {
	mu         sync.RWMutex
	readerWg   sync.WaitGroup
	evictionWg sync.WaitGroup
	ring       *ring.Ring

	value   interface{}
	expires int64
	hits    uint32
	state   uint32
}

func newRecord() *record {
	return &record{ring: ring.New(1)}
}

func (r *record) Close() error {
	r.readerWg.Done()
	return nil
}

func (r *record) Active() bool {
	return atomic.LoadUint32(&r.state) == stateActive
}

func (r *record) Evicting() bool {
	return atomic.LoadUint32(&r.state) == stateEvicting
}

func (r *record) Expired(now int64) bool {
	expires := atomic.LoadInt64(&r.expires)
	return expires > 0 && expires < now
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
	r.readerWg.Add(1)
	return r.value, true
}

func (r *record) setState(newState uint32) {
	prevState := (newState + 3 - 1) % 3
	if !atomic.CompareAndSwapUint32(&r.state, prevState, newState) {
		panic("evcache: invalid record state")
	}
}

func (r *record) init(value interface{}, ttl time.Duration) {
	r.value = value
	if ttl > 0 {
		atomic.StoreInt64(&r.expires, time.Now().Add(ttl).UnixNano())
	}
}
