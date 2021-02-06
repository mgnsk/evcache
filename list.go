package evcache

import (
	"container/ring"
	"sync"
)

type lfuRing struct {
	capacity uint32
	mu       sync.Mutex
	// cursor is the most frequently used record.
	// cursor.Next() is the least frequently used.
	cursor *ring.Ring
	length uint32
}

func newLFUList(capacity uint32) *lfuRing {
	l := &lfuRing{
		capacity: capacity,
	}
	return l
}

func (l *lfuRing) pop() *record {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cursor == nil || l.length <= l.capacity {
		return nil
	}
	front := l.cursor.Unlink(1)
	if front == front.Next() {
		l.cursor = nil
	}
	l.length--
	return front.Value.(*record)
}

//func (l *lfuList) pop() (key interface{}, success bool) {
//l.mu.Lock()
//defer l.mu.Unlock()
//if l.back != nil {
//front := l.back.Unlink(1)
//key = front.Value
//front.Value = nil
//// l.pool.Put(front)
//l.length--
//return key, true
//}
//return nil, false
//}

// TODO insert Ring element

func (l *lfuRing) insert(r *ring.Ring) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cursor != nil {
		l.cursor.Link(r)
	}
	l.cursor = r
	// TODO Length?
	l.length++
}

//func (l *lfuList) insert(key interface{}) *ring.Ring {
//r := l.pool.Get().(*ring.Ring)
//l.mu.Lock()
//defer l.mu.Unlock()
//r.Value = key
//if l.back == nil {
//l.back = r
//} else {
//l.back.Link(r)
//}
//l.back = r
//l.length++
//return r
//}

func (l *lfuRing) remove(r *ring.Ring) {
	l.mu.Lock()
	defer l.mu.Unlock()
	res := r.Prev().Unlink(1)
	if res != r {
		panic("evcache: invalid ring")
	}
	// r.Value = nil
	// l.pool.Put(r)
	l.length--
}

func (l *lfuRing) promote(r *ring.Ring, hits uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	//if l.length < 2 {
	//return
	//}
	//if l.back.Len() <= 1 {
	//return
	//}
	//if l.list.Len() <= 1 {
	//return
	//}
	// Move the record forward in the list
	// by hits delta or until end of list.

	// TODO call l.back.Move(1)

	// if 1 hit then 1 m ove?
	//
	// if hits > 0 && l.length > 1 && hits
	// how do we know how much till end (l.back)?
	if l.cursor == nil {
		l.cursor = r
		return
		// panic("lfuRing: cursor must not be nil")
	}

	// TODO move
	target := r
	//if target == target.Next() {
	//defer func() {
	//l.cursor = nil
	//}()
	//}

	if target == target.Next() {
		return
	}
	for i := uint32(0); i < hits; i++ {
		next := target.Next()
		if next == l.cursor {
			target = next
			// fmt.Println("hit back")
			break
		}
		target = next
	}
	// fmt.Println("promote")
	tmp := r.Prev().Unlink(1)
	if target == l.cursor {
		// fmt.Println("update back")
		l.cursor = tmp
		// TODO were losing l.back

		// need to remove the elem and link after target

		// l.list.MoveAfter(elem, target)
	}
	target.Link(tmp)
}
