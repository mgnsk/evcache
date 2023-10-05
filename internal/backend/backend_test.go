package backend_test

import (
	"testing"
	"time"

	"github.com/mgnsk/evcache/v3/internal/backend"
	. "github.com/onsi/gomega"
)

// Note: hardcoded realloc threshold value in the backend.
const size = 100000

func newBackend(size int) *backend.Backend[int, int] {
	b := backend.NewBackend[int, int](0)

	for i := 0; i < size; i++ {
		elem := backend.NewElement(i)
		b.LoadOrStore(i, elem)
		b.Lock()
		b.PushBack(elem, 0)
		b.Unlock()
	}

	return b
}

func TestUnlimitedCapacityMapShrink(t *testing.T) {
	t.Run("no realloc", func(t *testing.T) {
		g := NewWithT(t)

		b := newBackend(size)
		g.Expect(b.Len()).To(Equal(size))
		g.Expect(getMapLen(b)).To(Equal(size))

		// Evict half-1 of records.
		for i := 0; i < size/2-1; i++ {
			b.Evict(i)
		}

		b.RunGC(time.Now().UnixNano())

		g.Expect(b.Len()).To(Equal((size / 2) + 1))
		g.Expect(getMapLen(b)).To(Equal((size / 2) + 1))

		// TODO: assert that map is the same
	})

	t.Run("realloc", func(t *testing.T) {
		g := NewWithT(t)

		b := newBackend(size)
		g.Expect(b.Len()).To(Equal(size))
		g.Expect(getMapLen(b)).To(Equal(size))

		// Evict half of records.
		for i := 0; i < size/2; i++ {
			b.Evict(i)
		}

		b.RunGC(time.Now().UnixNano())

		g.Expect(b.Len()).To(Equal(size / 2))
		g.Expect(getMapLen(b)).To(Equal(size / 2))

		// TODO: assert that map is new
	})
}

func TestUnlimitedCapacityMapShrinkUninitializedRecords(t *testing.T) {
	t.Run("realloc", func(t *testing.T) {
		g := NewWithT(t)

		b := newBackend(size - 1)
		g.Expect(b.Len()).To(Equal(size - 1))
		g.Expect(getMapLen(b)).To(Equal(size - 1))

		// Store uninitialized record.
		elem := backend.NewElement(size - 1)
		b.LoadOrStore(size-1, elem)

		g.Expect(b.Len()).To(Equal(size-1), "list len only initialized records")
		g.Expect(getMapLen(b)).To(Equal(size), "map len also uninitialized")

		// Evict half of records.
		for i := 0; i < size/2; i++ {
			b.Evict(i)
		}

		b.RunGC(time.Now().UnixNano())

		g.Expect(b.Len()).To(Equal((size/2)-1), "list len only initialized records")
		g.Expect(getMapLen(b)).To(Equal(size/2), "map len also uninitialized")
	})
}

func getMapLen(b *backend.Backend[int, int]) int {
	n := 0
	b.Range(func(key int, elem backend.Element[int]) bool {
		n++
		return true
	})

	return n
}
