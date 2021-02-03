# evcache [![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache.svg)](https://pkg.go.dev/github.com/mgnsk/evcache)

This library is a work in progress.

## How it works

The cache is a wrapper for `sync.Map` which means that all its concurrency guarantees apply.
It additionally supports autoexpiry and capacity limit.

If the capacity is exceeded, the cache will begin evicting least frequently used records.
It takes at least 1 turnover of the full capacity to reach 100% sortedness of the evicted keys.

The cache does not prevent capacity from being exceeded - the maximum overflow
is the number of concurrent writers at any given moment.

To limit maximum overflow one must limit concurrency externally (for example with a channel semaphore).
