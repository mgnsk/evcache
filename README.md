# evcache

[![Go Reference](https://pkg.go.dev/badge/github.com/mgnsk/evcache/v4.svg)](https://pkg.go.dev/github.com/mgnsk/evcache/v4)
[![Go Report Card](https://goreportcard.com/badge/github.com/mgnsk/evcache/v4)](https://goreportcard.com/report/github.com/mgnsk/evcache/v4)

Package evcache implements a concurrent key-value cache with capacity overflow eviction, item expiry and deduplication.

### Example

```go
package main

import (
	"time"

	"github.com/mgnsk/evcache/v4"
)

func main() {
	c := evcache.New[string, string](
		evcache.WithCapacity(128),
		evcache.WithPolicy(evcache.LRU),
		evcache.WithTTL(time.Minute),
	)

	// Fetches an existing value or calls the callback to get a new value.
	result, err := c.Fetch("key", func() (string, error) {
		// Possibly a very long network call. It only blocks write access to this key.
		// Read access for this key returns as if the value does not exist.
		return "value", nil
	})

	if err != nil {
		panic(err)
	}

	// Use the result.
	println(result)
}
```
