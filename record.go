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
	active
	evicting
)

type record struct {
	mu         sync.RWMutex
	readerWg   sync.WaitGroup
	evictionWg sync.WaitGroup
	ring       *ring.Ring

	value    interface{}
	deadline int64
	hits     uint32
	state    uint32
}

func newRecord() *record {
	return &record{ring: ring.New(1)}
}

func (r *record) init(value interface{}, ttl time.Duration) {
	r.value = value
	if ttl > 0 {
		atomic.StoreInt64(&r.deadline, time.Now().Add(ttl).UnixNano())
	}
}

func (r *record) setState(newState uint32) bool {
	prevState := (newState + 3 - 1) % 3
	return atomic.CompareAndSwapUint32(&r.state, prevState, newState)
}

func (r *record) State() uint32 {
	return atomic.LoadUint32(&r.state)
}

func (r *record) Deadline() int64 {
	return atomic.LoadInt64(&r.deadline)
}

func (r *record) LoadAndHit() (interface{}, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.State() != active {
		return nil, false
	}
	atomic.AddUint32(&r.hits, 1)
	r.readerWg.Add(1)
	return r.value, true
}

func (r *record) Close() error {
	r.readerWg.Done()
	return nil
}
