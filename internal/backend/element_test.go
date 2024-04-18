package backend_test

import (
	"reflect"
	"testing"

	"github.com/mgnsk/evcache/v3/internal/backend"
	. "github.com/mgnsk/evcache/v3/internal/testing"
)

func TestRecordSize(t *testing.T) {
	rt := reflect.TypeOf(backend.Record[int, int]{})

	Equal(t, int(rt.Size()), 48)
}
