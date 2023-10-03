package backend_test

import (
	"testing"

	"github.com/mgnsk/evcache/v3/internal/backend"
	"github.com/mgnsk/list"
	. "github.com/onsi/gomega"
)

func TestMapShrink(t *testing.T) {
	g := NewWithT(t)

	b := backend.NewBackend[int, int](0)

	size := 100

	for i := 0; i < size; i++ {
		elem := list.NewElement(backend.Record[int]{Value: i})
		b.LoadOrStore(i, elem)
		b.PushBack(elem, 0)
	}

	g.Expect(b.Len()).To(Equal(size))
	g.Expect(b.Map()).To(HaveLen(size))

	// TODO: Assert that cap(m) >= len(m)

	// Evict half+1 records.
	for i := 0; i < size/2+1; i++ {
		b.Evict(i)
	}

	// TODO: Assert len(m) == cap(m)

	g.Expect(b.Map()).To(HaveLen(size/2 - 1))
}
