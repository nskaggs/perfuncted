package input

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
	"testing"
)

type fakeUinputOutputLister struct {
	outputs []output.Info
	err     error
	closed  bool
}

func (f *fakeUinputOutputLister) List(context.Context) ([]output.Info, error) {
	return f.outputs, f.err
}
func (f *fakeUinputOutputLister) Close() error { f.closed = true; return nil }

func TestRuntimeUinputResolvesFractionalLogicalOutputBeforeCreatingDevice(t *testing.T) {
	oldOpen, oldCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister = oldOpen; newUinputBackend = oldCreate })
	l := &fakeUinputOutputLister{outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}}
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) { return l, nil }
	calls := 0
	newUinputBackend = func(w, h int32) (Inputter, error) {
		calls++
		if w != 1600 || h != 900 {
			t.Errorf("virtual touchpad axes %dx%d, want logical 1600x900", w, h)
		}
		return noopInputter{}, nil
	}
	_, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
	if err != nil || calls != 1 || !l.closed {
		t.Fatalf("err=%v calls=%d outputClosed=%v", err, calls, l.closed)
	}
}
func TestRuntimeUinputFailsClosedWithoutOutputGeometry(t *testing.T) {
	oldOpen, oldCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister = oldOpen; newUinputBackend = oldCreate })
	l := &fakeUinputOutputLister{outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 0, H: 0}}}}
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) { return l, nil }
	newUinputBackend = func(int32, int32) (Inputter, error) {
		t.Fatal("invalid device must not be created")
		return nil, errors.New("should not happen")
	}
	_, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
	if err == nil || !l.closed {
		t.Fatalf("err=%v closed=%v", err, l.closed)
	}
}
