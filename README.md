# evcache

[![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache/v3.svg)](https://pkg.go.dev/github.com/mgnsk/evcache/v3)
[![Go Report Card](https://goreportcard.com/badge/github.com/mgnsk/evcache/v3)](https://goreportcard.com/report/github.com/mgnsk/evcache/v3)

`import "github.com/mgnsk/evcache/v3"`

Package evcache implements a concurrent key-value cache with capacity overflow eviction, item expiry and deduplication.

### Example

```go
c := evcache.New[string, string](10)

// Fetches an existing value or calls the callback to get a new value.
result, err := c.Fetch("key", time.Minute, func() (string, error) {
	// Possibly a very long network call. It only blocks write access to this key.
	// Read access to the key is always non-blocking.
	return "value", nil
})

if err != nil {
    return err
}

// Use the result.
_ = result
```
