package composition_test

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// Freeze the entire caller-visible surface, not just a forbidden method name.
// A renamed Host accessor, an any-returning escape hatch, or an embedded Host
// must require an explicit architecture review too.
type assemblyBoundary interface {
	Catalog() *tools.Catalog
	Service() composition.Service
	Store() application.EventStore
	Ready() bool
	Done() <-chan struct{}
	ServeACP(context.Context, io.ReadCloser, io.Writer) error
	Close() error
}

var _ assemblyBoundary = (*composition.Assembly)(nil)

func TestAssemblyExposesOnlyManagedCapabilitiesAndLifecycleObservation(t *testing.T) {
	want := reflect.TypeFor[assemblyBoundary]()
	got := reflect.TypeFor[*composition.Assembly]()
	for index := 0; index < got.NumMethod(); index++ {
		method := got.Method(index)
		if _, ok := want.MethodByName(method.Name); !ok {
			t.Errorf("unreviewed Assembly method %s exposes an additional capability", method.Name)
		}
	}
	for index := 0; index < got.Elem().NumField(); index++ {
		field := got.Elem().Field(index)
		if field.IsExported() {
			t.Errorf("Assembly field %s bypasses its managed accessors", field.Name)
		}
	}
}
