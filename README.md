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
dispose of stored values after all usage has stopped.

### Transactions and eviction mode

The cache uses waitgroups wrapped in `io.Closer` to track records returned to the user which forms a transaction.
Transactions behave depending of `EvictionMode`. There are two kinds of modes: the `ModeNonBlocking` which is the default mode and `ModeBlocking`.

#### Non-blocking mode

In `ModeNonBlocking` mode, an active transaction does not prevent a key from being concurrently overwritten.

For example, if there are multiple goroutines running `Cache.Fetch` in a loop and a key expires, the next
fetcher will set a new value which will eventually propagate to all loops.

The hot swap is unnoticeable while in the background, the eviction callback waits for transactions
for the old value to finish and then runs.

It is not safe to use `Cache.Evict` concurrently in this mode - the concurrent new value may be falsely evicted!
To safely evict records in the non-blocking mode see the documentation on `Cache.CompareAndEvict`.

#### Blocking mode

In `ModeBlocking` mode, an active transaction and eviction callback prevent a key from being concurrently overwritten.

The swap is noticeable to users since all calls to `Fetch` and `Set` for that key will block until the transactions
and callback finish.

It is guaranteed that while holding an active transaction, a concurrent call to `Cache.Evict` for that key returns the exact
same value to the first caller. Subsequent callers, in a transaction for that key, return false.
