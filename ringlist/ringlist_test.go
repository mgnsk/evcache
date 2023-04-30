package ringlist_test

import (
	"testing"

	"github.com/mgnsk/evcache/v3/ringlist"
	. "github.com/onsi/gomega"
)

func TestPushFront(t *testing.T) {
	var list ringlist.ElementList[int]

	g := NewWithT(t)

	list.PushFront(ringlist.NewElement(0))
	g.Expect(list.Len()).To(Equal(1))

	list.PushFront(ringlist.NewElement(1))
	g.Expect(list.Len()).To(Equal(2))

	expectValidRing(g, &list)
}

func TestPushBack(t *testing.T) {
	var list ringlist.ElementList[int]

	g := NewWithT(t)

	list.PushFront(ringlist.NewElement(0))
	g.Expect(list.Len()).To(Equal(1))

	list.PushFront(ringlist.NewElement(1))
	g.Expect(list.Len()).To(Equal(2))

	expectValidRing(g, &list)
}

func TestMoveToFront(t *testing.T) {
	t.Run("moving the back element", func(t *testing.T) {
		var list ringlist.ElementList[string]

		g := NewWithT(t)

		list.PushBack(ringlist.NewElement("one"))
		list.PushBack(ringlist.NewElement("two"))
		list.MoveToFront(list.Back())

		expectValidRing(g, &list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("one"))
	})

	t.Run("moving the middle element", func(t *testing.T) {
		var list ringlist.ElementList[string]

		g := NewWithT(t)

		list.PushBack(ringlist.NewElement("one"))
		list.PushBack(ringlist.NewElement("two"))
		list.PushBack(ringlist.NewElement("three"))
		list.MoveToFront(list.Front().Next())

		expectValidRing(g, &list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("three"))
	})
}

func TestMoveToBack(t *testing.T) {
	t.Run("moving the front element", func(t *testing.T) {
		var list ringlist.ElementList[string]

		g := NewWithT(t)

		list.PushBack(ringlist.NewElement("one"))
		list.PushBack(ringlist.NewElement("two"))
		list.MoveToBack(list.Front())

		expectValidRing(g, &list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("one"))
	})

	t.Run("moving the middle element", func(t *testing.T) {
		var list ringlist.ElementList[string]

		g := NewWithT(t)

		list.PushBack(ringlist.NewElement("one"))
		list.PushBack(ringlist.NewElement("two"))
		list.PushBack(ringlist.NewElement("three"))
		list.MoveToBack(list.Front().Next())

		expectValidRing(g, &list)
		g.Expect(list.Front().Value).To(Equal("one"))
		g.Expect(list.Back().Value).To(Equal("two"))
	})
}

func TestMoveForward(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		var list ringlist.ElementList[string]

		g := NewWithT(t)

		list.PushBack(ringlist.NewElement("one"))
		list.PushBack(ringlist.NewElement("two"))
		list.Move(list.Front(), 3)

		expectValidRing(g, &list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("one"))
	})

	t.Run("not overflow", func(t *testing.T) {
		var list ringlist.ElementList[string]

		g := NewWithT(t)

		list.PushBack(ringlist.NewElement("one"))
		list.PushBack(ringlist.NewElement("two"))
		list.PushBack(ringlist.NewElement("three"))
		list.Move(list.Front(), 1)

		expectValidRing(g, &list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Front().Next().Value).To(Equal("one"))
		g.Expect(list.Back().Value).To(Equal("three"))
	})
}

func TestMoveBackwards(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		var list ringlist.ElementList[string]

		g := NewWithT(t)

		list.PushBack(ringlist.NewElement("one"))
		list.PushBack(ringlist.NewElement("two"))
		list.Move(list.Back(), -3)

		expectValidRing(g, &list)
		g.Expect(list.Front().Value).To(Equal("two"))
		g.Expect(list.Back().Value).To(Equal("one"))
	})

	t.Run("not overflow", func(t *testing.T) {
		var list ringlist.ElementList[string]

		g := NewWithT(t)

		list.PushBack(ringlist.NewElement("one"))
		list.PushBack(ringlist.NewElement("two"))
		list.PushBack(ringlist.NewElement("three"))
		list.Move(list.Back(), -1)

		expectValidRing(g, &list)
		g.Expect(list.Front().Value).To(Equal("one"))
		g.Expect(list.Front().Next().Value).To(Equal("three"))
		g.Expect(list.Back().Value).To(Equal("two"))
	})
}

func TestDo(t *testing.T) {
	var list ringlist.ElementList[string]

	g := NewWithT(t)

	list.PushBack(ringlist.NewElement("one"))
	list.PushBack(ringlist.NewElement("two"))
	list.PushBack(ringlist.NewElement("three"))

	g.Expect(list.Len()).To(Equal(3))
	expectValidRing(g, &list)

	var elems []string
	list.Do(func(e *ringlist.Element[string]) bool {
		elems = append(elems, e.Value)
		return true
	})

	g.Expect(elems).To(Equal([]string{"one", "two", "three"}))
}

func expectValidRing[T any](g *WithT, list *ringlist.ElementList[T]) {
	g.Expect(list.Len()).To(BeNumerically(">", 0))
	g.Expect(list.Front()).To(Equal(list.Back().Next()))
	g.Expect(list.Back()).To(Equal(list.Front().Prev()))

	{
		expectedFront := list.Front()

		front := list.Front()

		for i := 0; i < list.Len(); i++ {
			front = front.Next()
		}

		g.Expect(front).To(Equal(expectedFront))
	}

	{
		expectedBack := list.Back()

		back := list.Back()

		for i := 0; i < list.Len(); i++ {
			back = back.Prev()
		}

		g.Expect(back).To(Equal(expectedBack))
	}
}
