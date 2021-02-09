# evcache [![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache.svg)](https://pkg.go.dev/github.com/mgnsk/evcache)

This README is a work in progress.

## How it works

The cache is a wrapper for `sync.Map` with autoexpiry, capacity limit and near-LFU eviction.

Writes to the cache do not block other reads. Each key uses its own RWMutex.
