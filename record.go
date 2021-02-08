package evcache

import (
	"container/ring"
	"sync"
	"sync/atomic"
	"time"
)

type record struct {
	mu   sync.RWMutex
	wg   sync.WaitGroup
	ring *ring.Ring

	value   interface{}
	expires int64
	alive   bool
	hits    uint32
}

func (r *record) Close() error {
	r.wg.Done()
	return nil
}

func (r *record) Load() (interface{}, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.alive {
		return nil, false
	}
	r.wg.Add(1)
	atomic.AddUint32(&r.hits, 1)
	return r.value, true
}

func (r *record) LoadAndDelete() (interface{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.alive {
		return nil, false
	}
	value := r.value
	r.value = nil
	r.expires = 0
	r.alive = false
	r.hits = 0
	return value, true
}

func (r *record) init(value interface{}, ttl time.Duration) {
	r.value = value
	if ttl > 0 {
		r.expires = time.Now().Add(ttl).UnixNano()
	}
	r.alive = true
	r.hits = 1
	r.wg.Add(1)
}
