package evcache

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

type record struct {
	mu      sync.RWMutex
	value   interface{}
	elem    *list.Element
	wg      sync.WaitGroup
	deleted uint32
	hits    uint64
	ttl     time.Duration
	expires int64
}

func (r *record) Close() error {
	r.wg.Done()
	return nil
}

func (r *record) load() (interface{}, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.isDeleted() {
		return nil, false
	}
	r.wg.Add(1)
	atomic.AddUint64(&r.hits, 1)
	return r.value, true
}

func (r *record) isDeleted() bool {
	return atomic.LoadUint32(&r.deleted) != 0
}

func (r *record) delete() {
	atomic.StoreUint32(&r.deleted, 1)
}

func (r *record) isExpired(now int64) bool {
	return r.expires > 0 && r.expires < now
}

func (r *record) touch() {
	atomic.StoreUint32(&r.deleted, 0)
	r.expires = time.Now().Add(r.ttl).UnixNano()
}
