# evcache [![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache.svg)](https://pkg.go.dev/github.com/mgnsk/evcache)

This README is a work in progress.

## How it works

The cache is a wrapper for `sync.Map` with autoexpiry, capacity limit and record ordering.
It acts like a non-blocking ordered map where writes do not block reads. Each value is wrapped
in a record type and uses its own RWMutex.

The default order is insertion order. Records are always inserted to the back of cache's list and
when overflow occurs, the front element is evicted.

When LFU ordering is used, then records which had accumulated hits since its last move
will be periodically moved forwards in the list based on the hit count delta.
Eventually the list becomes LFU ordered.

The cache provides an eviction callback for each record which can be used to safely
close stored values like network connections after all usage has stopped. The usage is tracked
by returning a waitgroup wrapped in `io.Closer` whenever a record is read which
if closed calls `wg.Done()`. If a record is evicted or expires while there are active users,
a new record may be stored under the same key while the old record value still exists referenced by active users
and awaiting eviction callback.
