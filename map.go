package evcache

import (
	"sync"
)

// syncMapWrapper wraps sync.Map. The zero syncMapWrapper is empty and ready to use.
type syncMapWrapper[K comparable, V any] struct {
	xmap sync.Map
}

func (w *syncMapWrapper[K, V]) Load(key K) (value V, ok bool) {
	if v, ok := w.xmap.Load(key); ok {
		return v.(V), true
	}
	return *new(V), false
}

func (w *syncMapWrapper[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	if v, ok := w.xmap.LoadOrStore(key, value); ok {
		return v.(V), true
	}
	return value, false
}

func (w *syncMapWrapper[K, V]) Delete(key K) {
	w.xmap.Delete(key)
}

func (w *syncMapWrapper[K, V]) Range(f func(key K, value V) bool) {
	w.xmap.Range(func(key, value any) bool {
		return f(key.(K), value.(V))
	})
}
