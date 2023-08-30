package evcache

import "sync"

type builtinMap[K comparable, V any] map[K]V

func (m builtinMap[K, V]) LoadOrStore(mu *sync.RWMutex, key K, value V) (actual V, loaded bool) {
	mu.RLock()
	if v, ok := m[key]; ok {
		mu.RUnlock()
		return v, true
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()

	if v, ok := m[key]; ok {
		return v, true
	}

	m[key] = value

	return value, false
}

func (m builtinMap[K, V]) Range(mu *sync.RWMutex, f func(key K, value V) bool) {
	mu.RLock()
	keys := make([]K, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	mu.RUnlock()

	for _, key := range keys {
		mu.RLock()
		v, ok := m[key]
		mu.RUnlock()
		if ok && !f(key, v) {
			return
		}
	}
}
