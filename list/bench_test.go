package list_test

import (
	"container/list"
	"testing"

	ringlist "github.com/mgnsk/evcache/v4/list"
)

func BenchmarkInsertDelete(b *testing.B) {
	b.Run("evcache list", func(b *testing.B) {
		var l ringlist.List[string]

		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			e := l.PushBack("a")
			l.Remove(e)
		}
	})

	b.Run("std list", func(b *testing.B) {
		l := list.New()

		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			e := l.PushBack("a")
			l.Remove(e)
		}
	})
}

func BenchmarkMoveAfter(b *testing.B) {
	b.Run("evcache list", func(b *testing.B) {
		var l ringlist.List[string]

		l.PushBack("a")
		l.PushBack("b")

		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			front := l.Front()
			l.MoveAfter(front, front.Next())
		}
	})

	b.Run("std list", func(b *testing.B) {
		l := list.New()

		l.PushBack("a")
		l.PushBack("b")

		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			front := l.Front()
			l.MoveAfter(front, front.Next())
		}
	})
}
