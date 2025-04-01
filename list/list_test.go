package list_test

import (
	"reflect"
	"testing"

	"github.com/mgnsk/evcache/v4/list"
)

func TestPushFront(t *testing.T) {
	var l list.List[int]

	l.PushFront(0)
	assertEqual(t, l.Len(), 1)

	l.PushFront(1)
	assertEqual(t, l.Len(), 2)

	expectHasExactElements(t, &l, 1, 0)
	assertEqual(t, l.Front().Value, 1)
	assertEqual(t, l.Back().Value, 0)
}

func TestPushBack(t *testing.T) {
	var l list.List[int]

	l.PushBack(0)
	assertEqual(t, l.Len(), 1)

	l.PushBack(1)
	assertEqual(t, l.Len(), 2)

	expectHasExactElements(t, &l, 0, 1)
	assertEqual(t, l.Front().Value, 0)
	assertEqual(t, l.Back().Value, 1)
}

func TestMoveToFront(t *testing.T) {
	t.Run("moving the back element", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.MoveToFront(l.Back())

		assertEqual(t, l.Len(), 2)
		expectHasExactElements(t, &l, "two", "one")
		assertEqual(t, l.Front().Value, "two")
		assertEqual(t, l.Back().Value, "one")
	})

	t.Run("moving the middle element", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.PushBack("three")
		l.MoveToFront(l.Front().Next())

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "two", "one", "three")
		assertEqual(t, l.Front().Value, "two")
		assertEqual(t, l.Back().Value, "three")
	})

	t.Run("moving the front element", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.PushBack("three")
		l.MoveToFront(l.Front())

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "one", "two", "three")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "three")
	})
}

func TestMoveToBack(t *testing.T) {
	t.Run("moving the front element", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.MoveToBack(l.Front())

		assertEqual(t, l.Len(), 2)
		expectHasExactElements(t, &l, "two", "one")
		assertEqual(t, l.Front().Value, "two")
		assertEqual(t, l.Back().Value, "one")
	})

	t.Run("moving the middle element", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.PushBack("three")
		l.MoveToBack(l.Front().Next())

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "one", "three", "two")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "two")
	})

	t.Run("moving the back element", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.PushBack("three")
		l.MoveToBack(l.Back())

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "one", "two", "three")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "three")
	})
}

func TestMoveBefore(t *testing.T) {
	t.Run("before middle", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		two := l.PushBack("two")
		three := l.PushBack("three")
		l.MoveBefore(three, two)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "one", "three", "two")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "two")
	})

	t.Run("before front", func(t *testing.T) {
		var l list.List[string]

		one := l.PushBack("one")
		two := l.PushBack("two")
		l.PushBack("three")
		l.MoveBefore(two, one)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "two", "one", "three")
		assertEqual(t, l.Front().Value, "two")
		assertEqual(t, l.Back().Value, "three")
	})

	t.Run("before itself", func(t *testing.T) {
		var l list.List[string]

		one := l.PushBack("one")
		l.PushBack("two")
		l.PushBack("three")

		l.MoveBefore(one, one)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "one", "two", "three")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "three")
	})

	t.Run("single-element list", func(t *testing.T) {
		var l list.List[string]

		one := l.PushBack("one")

		l.MoveBefore(one, one)

		assertEqual(t, l.Len(), 1)
		expectHasExactElements(t, &l, "one")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "one")
	})
}

func TestMoveAfter(t *testing.T) {
	t.Run("after middle", func(t *testing.T) {
		var l list.List[string]

		one := l.PushBack("one")
		two := l.PushBack("two")
		l.PushBack("three")
		l.MoveAfter(one, two)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "two", "one", "three")
		assertEqual(t, l.Front().Value, "two")
		assertEqual(t, l.Back().Value, "three")
	})

	t.Run("after tail", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		two := l.PushBack("two")
		three := l.PushBack("three")
		l.MoveAfter(two, three)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "one", "three", "two")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "two")
	})

	t.Run("after itself", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		three := l.PushBack("three")
		l.MoveAfter(three, three)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "one", "two", "three")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "three")
	})

	t.Run("single-element list", func(t *testing.T) {
		var l list.List[string]

		one := l.PushBack("one")

		l.MoveAfter(one, one)

		assertEqual(t, l.Len(), 1)
		expectHasExactElements(t, &l, "one")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "one")
	})
}

func TestMoveForward(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.PushBack("three")
		l.Move(l.Front(), 3)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "two", "three", "one")
		assertEqual(t, l.Front().Value, "two")
		assertEqual(t, l.Back().Value, "one")
	})

	t.Run("not overflow", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.PushBack("three")
		l.Move(l.Front(), 1)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "two", "one", "three")
		assertEqual(t, l.Front().Value, "two")
		assertEqual(t, l.Back().Value, "three")
	})
}

func TestMoveBackwards(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.PushBack("three")
		l.Move(l.Back(), -3)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "three", "one", "two")
		assertEqual(t, l.Front().Value, "three")
		assertEqual(t, l.Back().Value, "two")
	})

	t.Run("not overflow", func(t *testing.T) {
		var l list.List[string]

		l.PushBack("one")
		l.PushBack("two")
		l.PushBack("three")
		l.Move(l.Back(), -1)

		assertEqual(t, l.Len(), 3)
		expectHasExactElements(t, &l, "one", "three", "two")
		assertEqual(t, l.Front().Value, "one")
		assertEqual(t, l.Back().Value, "two")
	})
}

func TestDo(t *testing.T) {
	var l list.List[string]

	l.PushBack("one")
	l.PushBack("two")
	l.PushBack("three")

	assertEqual(t, l.Len(), 3)

	var elems []string
	l.Do(func(e *list.Element[string]) bool {
		elems = append(elems, e.Value)
		return true
	})

	assertEqual(t, elems, []string{"one", "two", "three"})
}

func expectHasExactElements[T any](t testing.TB, l *list.List[T], elements ...T) {
	t.Helper()

	var elems []T

	l.Do(func(e *list.Element[T]) bool {
		elems = append(elems, e.Value)

		return true
	})

	assertEqual(t, elems, elements)
}

func assertEqual[T any](t testing.TB, a, b T) {
	t.Helper()

	if !reflect.DeepEqual(a, b) {
		t.Fatalf("expected '%v' to equal '%v'", a, b)
	}
}
