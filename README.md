# evcache [![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache.svg)](https://pkg.go.dev/github.com/mgnsk/evcache)

## How it works

The cache is a wrapper for `sync.Map` with autoexpiry, capacity limit and near-LFU eviction.
