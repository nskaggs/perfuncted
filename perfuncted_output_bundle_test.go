package perfuncted_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/output"
)

type outputSpyLister struct {
	listCalls  int
	closeCalls int
	ctxValue   any
	infos      []output.Info
	err        error
	closeErr   error
}

func (o *outputSpyLister) List(ctx context.Context) ([]output.Info, error) {
	o.listCalls++
	o.ctxValue = ctx.Value(contextKey{})
	return o.infos, o.err
}

func (o *outputSpyLister) Close() error {
	o.closeCalls++
	return o.closeErr
}

func TestOutputBundleListAndClose(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), contextKey{}, "token")
	closeErr := errors.New("output close failed")
	spy := &outputSpyLister{
		infos: []output.Info{{
			Name:        "HDMI-A-1",
			Backend:     "wayland",
			Geometry:    output.Geometry{X: 0, Y: 0, W: 1920, H: 1080},
			ResolutionW: 1920,
			ResolutionH: 1080,
			Scale:       2,
		}},
		closeErr: closeErr,
	}

	pf := &perfuncted.Perfuncted{
		Output: perfuncted.OutputBundle{Lister: spy},
	}

	got, err := pf.Output.List(ctx)
	if err != nil {
		t.Fatalf("Output.List: %v", err)
	}
	if spy.listCalls != 1 {
		t.Fatalf("List calls = %d, want 1", spy.listCalls)
	}
	if spy.closeCalls != 0 {
		t.Fatalf("Close calls after List = %d, want 0", spy.closeCalls)
	}
	if spy.ctxValue != "token" {
		t.Fatalf("List context value = %v, want token", spy.ctxValue)
	}
	if len(got) != 1 {
		t.Fatalf("Output.List len = %d, want 1", len(got))
	}
	if got[0].Name != "HDMI-A-1" || got[0].Backend != "wayland" {
		t.Fatalf("Output.List returned %+v, want HDMI-A-1/wayland", got[0])
	}
	if got[0].Geometry.W != 1920 || got[0].Geometry.H != 1080 {
		t.Fatalf("Output.List geometry = %+v, want 1920x1080", got[0].Geometry)
	}

	if err := pf.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want %v", err, closeErr)
	}
	if spy.closeCalls != 1 {
		t.Fatalf("Close calls after Perfuncted.Close = %d, want 1", spy.closeCalls)
	}
}
