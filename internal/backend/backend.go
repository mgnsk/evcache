/*
Package backend implements cache backend.
*/
package backend

import (
	"maps"
	"sync"
	"time"

	"github.com/mgnsk/evcache/v4/list"
)

// Available cache eviction policies.
const (
	FIFO = "fifo"
	LFU  = "lfu"
	LRU  = "lru"
)

// Backend implements cache backend.
type Backend[K comparable, V any] struct {
	done             chan struct{}
	timer            *time.Timer                       // timer until the next element expiry.
	xmap             map[K]*list.Element[Record[K, V]] // map of uninitialized and initialized elements
	list             list.List[Record[K, V]]           // list of initialized elements
	policy           string
	lastGCAt         int64
	earliestExpireAt int64
	cap              int
	defaultTTL       time.Duration
	debounce         time.Duration
	lastLen          int
	numDeleted       uint64
	mu               sync.Mutex
	gcStarted        bool
}

// Init initializes the cache.
func (b *Backend[K, V]) Init(capacity int, policy string, defaultTTL time.Duration, debounce time.Duration) {
	t := time.NewTimer(0)
	<-t.C

	b.timer = t
	b.done = make(chan struct{})
	b.xmap = make(map[K]*list.Element[Record[K, V]], capacity)
	b.cap = capacity
	b.policy = policy
	b.defaultTTL = defaultTTL
	b.debounce = debounce
}

// Close stops the backend cleanup loop
// and allows the cache backend to be garbage collected.
func (b *Backend[K, V]) Close() {
	close(b.done)
}

// Len returns the number of initialized values.
func (b *Backend[K, V]) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.list.Len()
}

// Has returns whether the value for key is initialized.
func (b *Backend[K, V]) Has(key K) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	elem, ok := b.xmap[key]
	return ok && elem.Value.state == stateInitialized
}

// Load an initialized value.
func (b *Backend[K, V]) Load(key K) (value V, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if elem, ok := b.xmap[key]; ok && elem.Value.state == stateInitialized {
		b.hit(elem)
		return elem.Value.Value, true
	}

	var zero V
	return zero, false
}

// Range iterates over initialized cache key-value pairs in no particular order or consistency.
// If f returns false, range stops the iteration.
//
// f may modify the cache.
func (b *Backend[K, V]) Range(f func(key K, value V) bool) {
	b.mu.Lock()
	elems := make([]*list.Element[Record[K, V]], 0, len(b.xmap))
	for _, elem := range b.xmap {
		if elem.Value.state == stateInitialized {
			elems = append(elems, elem)
		}
	}
	b.mu.Unlock()

	for _, elem := range elems {
		if !f(elem.Value.Key, elem.Value.Value) {
			return
		}
	}
}

// Evict an initialized value.
func (b *Backend[K, V]) Evict(key K) (V, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var zero V

	if elem, ok := b.xmap[key]; ok && elem.Value.state == stateInitialized {
		b.delete(elem)
		return elem.Value.Value, true
	}

	return zero, false
}

// Store and initialize a value with the default TTL.
func (b *Backend[K, V]) Store(key K, value V) {
	b.StoreTTL(key, value, b.defaultTTL)
}

// StoreTTL stores and initializes a value with specified TTL.
func (b *Backend[K, V]) StoreTTL(key K, value V, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if elem, ok := b.xmap[key]; ok {
		switch elem.Value.state {
		case stateInitialized:
			b.delete(elem)

		default:
			delete(b.xmap, key)
			elem.Value.state = stateOverwritten
		}
	}

	// Note: unlike Fetch, Store never lets map readers
	// see uninitialized elements.

	new := list.NewElement(Record[K, V]{})
	deadline := b.prepareDeadline(ttl)
	new.Value.Initialize(key, value, deadline)
	b.list.PushBackElem(new)
	b.xmap[key] = new
	b.deleteOverflow()
}

// Fetch loads or stores (and initializes) a value for key with the default TTL.
// If a value exists, f will not be called, otherwise f will be called to fetch the new value.
// It panics if f panics. Concurrent fetches for the same key will block and return a single result.
func (b *Backend[K, V]) Fetch(key K, f func() (V, error)) (value V, err error) {
	return b.FetchTTL(key, func() (V, time.Duration, error) {
		value, err := f()
		return value, b.defaultTTL, err
	})
}

// FetchTTL loads or stores (and initializes) a value for key with the specified TTL.
// If a value exists, f will not be called, otherwise f will be called to fetch the new value.
// It panics if f panics. Concurrent fetches for the same key will block and return a single result.
func (b *Backend[K, V]) FetchTTL(key K, f func() (V, time.Duration, error)) (value V, err error) {
tryLoadStore:
	b.mu.Lock()
	if elem, ok := b.xmap[key]; ok {
		if elem.Value.state == stateInitialized {
			b.hit(elem)
			b.mu.Unlock()
			return elem.Value.Value, nil
		}

		b.mu.Unlock()
		elem.Value.wg.Wait()

		goto tryLoadStore
	}

	new := list.NewElement(Record[K, V]{})
	new.Value.wg.Add(1)
	b.xmap[key] = new
	b.mu.Unlock()

	defer new.Value.wg.Done()

	defer func() {
		if r := recover(); r != nil {
			b.mu.Lock()
			defer b.mu.Unlock()

			if new.Value.state == stateUninitialized {
				delete(b.xmap, key)
			}

			panic(r)
		}
	}()

	value, ttl, err := f()

	b.mu.Lock()
	defer b.mu.Unlock()

	if new.Value.state == stateOverwritten {
		// Already deleted from map by Store().
		return value, err
	}

	if err != nil {
		delete(b.xmap, key)

		return value, err
	}

	deadline := b.prepareDeadline(ttl)
	b.list.PushBackElem(new)
	new.Value.Initialize(key, value, deadline)
	b.deleteOverflow()

	return value, nil
}

func (b *Backend[K, V]) prepareDeadline(ttl time.Duration) int64 {
	var deadline int64

	if ttl > 0 {
		b.onceStartCleanupLoop()

		now := time.Now()
		deadline = now.Add(ttl).UnixNano()
		deadline = b.debounceDeadline(deadline)

		if b.earliestExpireAt == 0 || deadline < b.earliestExpireAt {
			b.earliestExpireAt = deadline
			after := time.Duration(deadline - now.UnixNano())
			b.timer.Reset(after)
		}
	}

	return deadline
}

func (b *Backend[K, V]) debounceDeadline(deadline int64) int64 {
	if until := deadline - b.lastGCAt; until < b.debounce.Nanoseconds() {
		deadline += b.debounce.Nanoseconds() - until
	}

	return deadline
}

func (b *Backend[K, V]) hit(elem *list.Element[Record[K, V]]) {
	switch b.policy {
	case LFU:
		if next := elem.Next(); next != nil {
			b.list.MoveAfter(elem, next)
		}
	case LRU:
		b.list.MoveToBack(elem)
	default:
	}
}

func (b *Backend[K, V]) deleteOverflow() {
	if b.cap > 0 && b.list.Len() > b.cap {
		b.delete(b.list.Front())
	}
}

// delete an initialized element.
func (b *Backend[K, V]) delete(elem *list.Element[Record[K, V]]) {
	delete(b.xmap, elem.Value.Key)
	b.list.Remove(elem)
	b.numDeleted++

	if b.lastLen == 0 {
		b.lastLen = len(b.xmap)
	}

	if b.numDeleted > uint64(b.lastLen)/2 {
		b.numDeleted = 0
		b.lastLen = len(b.xmap)
		b.xmap = maps.Clone(b.xmap)
	}
}

func (b *Backend[K, V]) onceStartCleanupLoop() {
	if b.gcStarted {
		return
	}

	b.gcStarted = true

	go func() {
		defer b.timer.Stop()

		for {
			select {
			case <-b.done:
				return

			case now := <-b.timer.C:
				b.DoCleanup(now.UnixNano())
			}
		}
	}()
}

// DoCleanup runs the cache cleanup.
func (b *Backend[K, V]) DoCleanup(nowNano int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var (
		expired  []*list.Element[Record[K, V]]
		earliest int64
	)

	b.list.Do(func(elem *list.Element[Record[K, V]]) bool {
		deadline := elem.Value.deadline

		if deadline > 0 && deadline < nowNano {
			expired = append(expired, elem)
		} else if deadline > 0 && (earliest == 0 || deadline < earliest) {
			earliest = deadline
		}

		return true
	})

	for _, elem := range expired {
		b.delete(elem)
	}

	b.lastGCAt = nowNano

	switch earliest {
	case 0:
		b.earliestExpireAt = 0

	default:
		earliest = b.debounceDeadline(earliest)
		b.earliestExpireAt = earliest
		b.timer.Reset(time.Duration(earliest - nowNano))
	}
}
