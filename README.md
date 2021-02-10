# evcache [![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache.svg)](https://pkg.go.dev/github.com/mgnsk/evcache)

This README is a work in progress.

## How it works

The cache is a wrapper for `sync.Map` with autoexpiry, capacity limit and eviction order.
It acts like a non-blocking ordered map where writes do not block reads. Each value is wrapped
in a record type and uses its own RWMutex.

The default record order is insertion order. Records are always inserted to the back of a list and
when overflow occurs, the front element is evicted.

When LFU ordering is used, then records which had accumulated hits since its last move
will be periodically moved forwards in the list based on the hit count delta.
Eventually the list becomes LFU ordered.
