package ringlist_test

import (
	"container/ring"
	"testing"

	"github.com/mgnsk/evcache/v2/ringlist"
	. "github.com/onsi/gomega"
)

func TestPushFrontOverflow(t *testing.T) {
	list := ringlist.New(1)

	g := NewWithT(t)

	front := list.PushFront(&ring.Ring{Value: "one"})
	g.Expect(front).To(BeNil())
	g.Expect(list.Len()).To(Equal(1))

	front = list.PushFront(&ring.Ring{Value: "two"})
	g.Expect(front).To(Equal("one"))
	g.Expect(list.Len()).To(Equal(1))
}

func TestPushBackOverflow(t *testing.T) {
	list := ringlist.New(1)

	g := NewWithT(t)

	front := list.PushBack(&ring.Ring{Value: "one"})
	g.Expect(front).To(BeNil())
	g.Expect(list.Len()).To(Equal(1))

	front = list.PushBack(&ring.Ring{Value: "two"})
	g.Expect(front).To(Equal("one"))
	g.Expect(list.Len()).To(Equal(1))
}

func TestMoveToFront(t *testing.T) {
	t.Run("moving the back element", func(t *testing.T) {
		list := ringlist.New(2)

		g := NewWithT(t)

		list.PushBack(&ring.Ring{Value: "one"})
		list.PushBack(&ring.Ring{Value: "two"})
		list.MoveToFront(list.Back())

		expectValidRing(g, list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("one"))
	})

	t.Run("moving the middle element", func(t *testing.T) {
		list := ringlist.New(3)

		g := NewWithT(t)

		list.PushBack(&ring.Ring{Value: "one"})
		list.PushBack(&ring.Ring{Value: "two"})
		list.PushBack(&ring.Ring{Value: "three"})
		list.MoveToFront(list.Front().Next())

		expectValidRing(g, list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("three"))
	})
}

func TestMoveToBack(t *testing.T) {
	t.Run("moving the front element", func(t *testing.T) {
		list := ringlist.New(2)

		g := NewWithT(t)

		list.PushBack(&ring.Ring{Value: "one"})
		list.PushBack(&ring.Ring{Value: "two"})
		list.MoveToBack(list.Front())

		expectValidRing(g, list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("one"))
	})

	t.Run("moving the middle element", func(t *testing.T) {
		list := ringlist.New(3)

		g := NewWithT(t)

		list.PushBack(&ring.Ring{Value: "one"})
		list.PushBack(&ring.Ring{Value: "two"})
		list.PushBack(&ring.Ring{Value: "three"})
		list.MoveToBack(list.Front().Next())

		expectValidRing(g, list)
		g.Expect(list.Front().Value).To(Equal("one"))
		g.Expect(list.Back().Value).To(Equal("two"))
	})
}

func TestMoveForward(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		list := ringlist.New(2)

		g := NewWithT(t)

		list.PushBack(&ring.Ring{Value: "one"})
		list.PushBack(&ring.Ring{Value: "two"})
		list.Move(list.Front(), 3)

		expectValidRing(g, list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("one"))
	})

	t.Run("not overflow", func(t *testing.T) {
		list := ringlist.New(3)

		g := NewWithT(t)

		list.PushBack(&ring.Ring{Value: "one"})
		list.PushBack(&ring.Ring{Value: "two"})
		list.PushBack(&ring.Ring{Value: "three"})
		list.Move(list.Front(), 1)

		expectValidRing(g, list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Front().Next().Value).To(Equal("one"))
		g.Expect(list.Back().Value).To(Equal("three"))
	})
}

func TestMoveBackwards(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		list := ringlist.New(2)

		g := NewWithT(t)

		list.PushBack(&ring.Ring{Value: "one"})
		list.PushBack(&ring.Ring{Value: "two"})
		list.Move(list.Back(), -3)

		expectValidRing(g, list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("one"))
	})

	t.Run("not overflow", func(t *testing.T) {
		list := ringlist.New(3)

		g := NewWithT(t)

		list.PushBack(&ring.Ring{Value: "one"})
		list.PushBack(&ring.Ring{Value: "two"})
		list.PushBack(&ring.Ring{Value: "three"})
		list.Move(list.Back(), -1)

		expectValidRing(g, list)
		g.Expect(list.Front().Value).To(Equal("one"))
		g.Expect(list.Front().Next().Value).To(Equal("three"))
		g.Expect(list.Back().Value).To(Equal("two"))
	})
}

func expectValidRing(g *WithT, list *ringlist.List) {
	g.Expect(list.Len()).To(BeNumerically(">", 0))
	g.Expect(list.Front()).To(Equal(list.Back().Next()))
	g.Expect(list.Back()).To(Equal(list.Front().Prev()))

	expectedFront := list.Front()

	front := list.Front()

	for i := 0; i < list.Len(); i++ {
		front = front.Next()
	}

	g.Expect(front).To(Equal(expectedFront))
}
