# evcache [![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache.svg)](https://pkg.go.dev/github.com/mgnsk/evcache)

In-memory cache with eventually consistent LFU eviction.

## How it works

The cache is a wrapper for `sync.Map` which means that all its concurrency guarantees apply.
It additionally supports autoexpiry and a size limit.

If the size limit is exceeded, the cache will start to evict least frequently used records.
It takes at least 1 turnover of all keys to reach 100% sortedness of the evicted keys.

The cache does not prevent size limit from being exceeded - the maximum overflow
is the number of concurrent writers at any given moment.

To limit maximum overflow one must limit concurrency externally (for example with a channel semaphore).

### TODO
* Once submitted to pkg.go.dev, add link here
* Improve the readme
