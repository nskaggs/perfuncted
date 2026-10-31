package pftest_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
)

type outputFixtureLifetime struct {
	infos  []output.Info
	err    error
	closed bool
}

func (o *outputFixtureLifetime) List(ctx context.Context) ([]output.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return o.infos, o.err
}
func (o *outputFixtureLifetime) Close() error { o.closed = true; return nil }

func TestNewWithOutputsOwnsOutputDiscoveryFailureAndTeardown(t *testing.T) {
	want := []output.Info{{Name: "Synthetic-A", Geometry: output.Geometry{X: -1920, Y: 0, W: 1920, H: 1080}, Available: true}, {Name: "Synthetic-B", Geometry: output.Geometry{X: 0, Y: 0, W: 2560, H: 1440}, Available: true}}
	backend := &outputFixtureLifetime{infos: want}
	session := pftest.NewWithOutputs(nil, nil, nil, backend, nil)
	ctx := context.Background()
	got, err := session.Outputs.List(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-output fixture lost physical coordinates: %+v %v", got, err)
	}
	failure := errors.New("synthetic display disconnected")
	backend.err = failure
	if _, err := session.Outputs.List(ctx); !errors.Is(err, failure) {
		t.Fatalf("output disconnect masked: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !backend.closed {
		t.Fatal("owned output backend was not closed")
	}
	if _, err := session.Outputs.List(ctx); err == nil {
		t.Fatal("closed output fixture still allowed discovery")
	}
}
