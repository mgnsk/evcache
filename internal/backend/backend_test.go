package backend_test

import (
	"testing"
	"time"

	"github.com/mgnsk/evcache/v3/internal/backend"
	. "github.com/onsi/gomega"
)

func TestMapShrinkUninitializedRecords(t *testing.T) {
	t.Run("realloc", func(t *testing.T) {
		g := NewWithT(t)

		b := newBackend(size - 1)
		g.Expect(b.Len()).To(Equal(size - 1))
		g.Expect(getInitializedMapLen(b)).To(Equal(size - 1))

		// Store uninitialized record.
		elem := b.Reserve()
		b.LoadOrStore(size-1, elem)

		g.Expect(b.Len()).To(Equal(size-1), "list len only initialized elements")
		g.Expect(getInitializedMapLen(b)).To(Equal(size-1), "map len only initialized elements")

		// Evict half of records.
		for i := 0; i < size/2; i++ {
			b.Evict(i)
		}

		b.RunGC(time.Now().UnixNano())

		// Initialize the record.
		b.Initialize(size-1, 0, 0)

		g.Expect(b.Len()).To(Equal((size / 2)), "list len only initialized elements")
		g.Expect(getInitializedMapLen(b)).To(Equal((size / 2)), "map len only initialized elements")
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
