package testing

import (
	"reflect"
	"testing"
	"time"
)

// Must that error did not occur.
func Must(t testing.TB, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("expected success")
	}
}

// Equal asserts that values are deeply equal.
func Equal[T any](t testing.TB, a, b T) {
	t.Helper()

	if !reflect.DeepEqual(a, b) {
		t.Fatalf("expected '%v' to be equal to '%v'", a, b)
	}
}

// EventuallyTrue asserts that f eventually returns true.
func EventuallyTrue(t testing.TB, f func() bool, timeout ...time.Duration) {
	t.Helper()

	limit := time.Second
	if len(timeout) > 0 {
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
