package evcache

type backendMap[K comparable, V any] interface {
	Load(K) (V, bool)
	Store(K, V)
	Delete(K)
	Len() int
	Range(func(K, V) bool)
}

type builtinMap[K comparable, V any] map[K]V

func (m builtinMap[K, V]) Load(key K) (value V, ok bool) {
	value, ok = m[key]
	return
}

func (m builtinMap[K, V]) Store(key K, value V) {
	m[key] = value
}

func (m builtinMap[K, V]) Delete(key K) {
	delete(m, key)
}

func (m builtinMap[K, V]) Len() int {
	return len(m)
}

func (m builtinMap[K, V]) Range(f func(key K, value V) bool) {
	for k, v := range m {
		if !f(k, v) {
			return
		}
	}
}
