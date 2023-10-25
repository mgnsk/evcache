package backend_test

import (
	"testing"
	"time"

	"github.com/mgnsk/evcache/v3/internal/backend"
	. "github.com/mgnsk/evcache/v3/internal/testing"
)

func TestMapShrinkUninitializedRecords(t *testing.T) {
	t.Run("realloc", func(t *testing.T) {
		b := newBackend(size - 1)
		AssertEqual(t, b.Len(), size-1)
		AssertEqual(t, getInitializedMapLen(b), size-1)

		// Store uninitialized record.
		elem := b.Reserve()
		b.LoadOrStore(size-1, elem)

		AssertEqual(t, b.Len(), size-1)
		AssertEqual(t, b.Len(), size-1)

		// Evict half of records.
		for i := 0; i < size/2; i++ {
			b.Evict(i)
		}

		b.RunGC(time.Now().UnixNano())

		// Initialize the record.
		b.Initialize(size-1, 0, 0)

		AssertEqual(t, b.Len(), size/2)
		AssertEqual(t, b.Len(), size/2)
	})
}

// Note: backend constant.
const size = 100000

func newBackend(size int) *backend.Backend[int, int] {
	b := backend.NewBackend[int, int](0)

	for i := 0; i < size; i++ {
		b.LoadOrStore(i, b.Reserve())
		b.Initialize(i, i, 0)
	}

	return b
}

func getInitializedMapLen(b *backend.Backend[int, int]) int {
	n := 0
	b.Range(func(key int, elem backend.Element[int]) bool {
		n++
		return true
	})

	return n
}
