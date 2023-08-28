package evcache

import (
	"sync"
)

type concurrentMap[K comparable, V any] interface {
	Load(key K) (value V, ok bool)
	LoadOrStore(key K, value V) (actual V, loaded bool)
	Delete(key K)
	Range(f func(key K, value V) bool)
}

var _ concurrentMap[string, string] = &rwMutexMap[string, string]{}
var _ concurrentMap[string, string] = &syncMapWrapper[string, string]{}

type rwMutexMap[K comparable, V any] struct {
	xmap map[K]V
	mu   sync.RWMutex
}

func newRWMutexMap[K comparable, V any](cap int) *rwMutexMap[K, V] {
	return &rwMutexMap[K, V]{
		xmap: make(map[K]V, cap),
	}
}

func (w *rwMutexMap[K, V]) Load(key K) (value V, ok bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	v, ok := w.xmap[key]
	return v, ok
}

func (w *rwMutexMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	w.mu.RLock()
	if v, ok := w.xmap[key]; ok {
		w.mu.RUnlock()
		return v, true
	}
	w.mu.RUnlock()

	w.mu.Lock()
	defer w.mu.Unlock()

	if v, ok := w.xmap[key]; ok {
		return v, true
	}

	w.xmap[key] = value

	return value, false
}

func (w *rwMutexMap[K, V]) Delete(key K) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.xmap, key)
}

func (w *rwMutexMap[K, V]) Range(f func(key K, value V) bool) {
	w.mu.RLock()
	keys := make([]K, 0, len(w.xmap))
	for k := range w.xmap {
		keys = append(keys, k)
	}
	w.mu.RUnlock()

	for _, key := range keys {
		if v, ok := w.Load(key); ok {
			if !f(key, v) {
				return
			}
		}
	}
}

// syncMapWrapper wraps sync.Map. The zero syncMapWrapper is empty and ready to use.
type syncMapWrapper[K comparable, V any] struct {
	xmap sync.Map
}

func newSyncMapWrapper[K comparable, V any]() *syncMapWrapper[K, V] {
	return &syncMapWrapper[K, V]{}
}

func (w *syncMapWrapper[K, V]) Load(key K) (value V, ok bool) {
	if v, ok := w.xmap.Load(key); ok {
		return v.(V), true
	}
	return *new(V), false
}

func (w *syncMapWrapper[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	v, ok := w.xmap.LoadOrStore(key, value)
	if ok {
		return v.(V), true
	}

	return *new(V), false
}

func (w *syncMapWrapper[K, V]) Delete(key K) {
	w.xmap.Delete(key)
}

func (w *syncMapWrapper[K, V]) Range(f func(key K, value V) bool) {
	w.xmap.Range(func(key, value any) bool {
		return f(key.(K), value.(V))
	})
}
