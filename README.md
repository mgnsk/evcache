# evcache

[![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache/v3.svg)](https://pkg.go.dev/github.com/mgnsk/evcache/v3)
[![Go Report Card](https://goreportcard.com/badge/github.com/mgnsk/evcache)](https://goreportcard.com/report/github.com/mgnsk/evcache)

[Benchmarks](https://mgnsk.github.io/evcache/dev/bench)

`import "github.com/mgnsk/evcache/v3"`

Package evcache implements a key-value cache with capacity overflow eviction, item expiry and deduplication.

### Example

```go
c := evcache.NewWithCapacity[string, string](10)

// Fetches an existing value or calls the callback to get a new value.
result, err := c.Fetch("key", time.Minute, func() (string, error) {
	return "value", nil
})

if err != nil {
    return err
}

// Use the result.
_ = result
```
