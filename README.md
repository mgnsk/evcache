# evcache

[![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache/v2.svg)](https://pkg.go.dev/github.com/mgnsk/evcache/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/mgnsk/evcache)](https://goreportcard.com/report/github.com/mgnsk/evcache)
[![Maintainability](https://api.codeclimate.com/v1/badges/2d6db0eb1dc3cbe2848c/maintainability)](https://codeclimate.com/github/mgnsk/evcache/maintainability)
[![codecov](https://codecov.io/gh/mgnsk/evcache/branch/master/graph/badge.svg?token=8S4JNGTOST)](https://codecov.io/gh/mgnsk/evcache)


[Benchmarks](https://mgnsk.github.io/evcache/dev/bench)

## How it works

The cache is a wrapper for `sync.Map` with autoexpiry, capacity limit and record ordering.
It is similar to a non-blocking ordered map where writes do not block reads. Each value is wrapped
in a record type and uses its own RWMutex.

### Record ordering

The default order is insertion order. Records are inserted to the back of cache's list and
when overflow occurs, the front element is evicted.

When LFU ordering is used, then records which had accumulated hits since its last move
will be periodically moved forwards in the list based on the hit count delta.
Eventually the list becomes LFU ordered.

### Eviction callback

The cache provides an eviction callback for each record which can be used to safely
dispose of stored values after all usage has stopped. The usage is tracked by returning a waitgroup
wrapped in `io.Closer` whenever a record is read which when closed, calls `wg.Done()`.

There are two modes of eviction: the `ModeNonBlocking` which is the default mode and `ModeBlocking`.

#### Non-blocking mode

In the non-blocking mode, an evicted record whose is currently being held active or whose
asynchronous eviction callback has not run yet will not block the cache key from being overwritten with a new value.

This is useful when the stored value needs to be hot-swapped without creating a pause
during concurrent usage. It allows new users to continue with the new value
while the eviction callback for old value waits for users of the old value to return.

To safely evict records in the non-blocking mode under concurrent usage, `Cache.CompareAndEvict`
must be used. Read the documentation of that method for more info.

#### Blocking mode

In the blocking mode, `Fetch` and `Set` wait for all readers of old value to finish
and `EvictionCallback` to have run before writing a new value.

If a user is holding an active value (has not closed the io.Closer yet)
it is guaranteed that Evict returns the same exact value for the first concurrent user
who called Evict for that key. The key is prevented from being overwritten
until all users close the closers.
