package backend

// Policy is a cache eviction policy.
type Policy int

// Available cache eviction policies.
const (
	Default Policy = iota
	LFU
	LRU
)
