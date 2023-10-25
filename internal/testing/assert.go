package testing

import (
	"reflect"
	"testing"
	"time"
)

// AssertSuccess that error did not occur.
func AssertSuccess(t testing.TB, err error) {
	if err != nil || !isNil(err) {
		t.Fatalf("expected success")
	}
}

// AssertEqual asserts that values are deeply equal.
func AssertEqual[T any](t testing.TB, a, b T) {
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("expected '%v' to be equal to '%v'", a, b)
	}
}

// AssertEventuallyTrue asserts that f eventually returns true.
func AssertEventuallyTrue(t testing.TB, f func() bool, timeout ...time.Duration) {
	limit := time.Second
	if timeout != nil {
		limit = timeout[0]
	}

	timer := time.NewTimer(limit)
	defer timer.Stop()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			t.Fatalf("timeout: expected eventually to be true")

		case <-ticker.C:
			if f() {
				return
			}
		}
	}
}

func isNil(a interface{}) bool {
	if a == nil {
		return true
	}

	switch reflect.TypeOf(a).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return reflect.ValueOf(a).IsNil()
	}

	return false
}
