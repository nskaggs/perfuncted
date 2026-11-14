//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
)

// Constructor failure must not panic during optional output enumeration or
// proceed to create a kernel input device on an uninitialized lister.
func TestUinputOutputDiscoveryRejectsUninitializedLister(t *testing.T) {
	previousOpen, previousCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister, newUinputBackend = previousOpen, previousCreate })
	for _, tc := range []struct {
		name string
		open func(context.Context, env.Runtime) (output.Lister, error)
	}{
		{name: "nil interface", open: func(context.Context, env.Runtime) (output.Lister, error) { return nil, nil }},
		{name: "typed nil", open: func(context.Context, env.Runtime) (output.Lister, error) {
			var v *fakeUinputOutputLister
			return v, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			openUinputOutputLister = tc.open
			newUinputBackend = func(int32, int32) (Inputter, error) {
				t.Fatal("uninitialized output metadata authorized an input device")
				return nil, nil
			}
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("uninitialized output lister caused panic: %v", p)
				}
			}()
			device, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
			if device != nil || err == nil || !strings.Contains(err.Error(), "output") {
				t.Fatalf("uninitialized lister returned backend=%T err=%v", device, err)
			}
		})
	}
}

// An enumeration or teardown error must veto device creation, and release
// any successfully acquired output lister exactly once.
type failingUinputLister struct {
	outputs           []output.Info
	listErr, closeErr error
	closes            int
}

func (l *failingUinputLister) List(context.Context) ([]output.Info, error) {
	return l.outputs, l.listErr
}
func (l *failingUinputLister) Close() error { l.closes++; return l.closeErr }
func TestUinputOutputDiscoveryFailureClosesListerAndRejectsDevice(t *testing.T) {
	previousOpen, previousCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister, newUinputBackend = previousOpen, previousCreate })
	for _, tc := range []struct {
		name              string
		listErr, closeErr error
	}{
		{"list failure", errors.New("list failed"), nil},
		{"close failure", nil, errors.New("close failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &failingUinputLister{outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1920, H: 1080}}}, listErr: tc.listErr, closeErr: tc.closeErr}
			openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) { return l, nil }
			newUinputBackend = func(int32, int32) (Inputter, error) {
				t.Fatal("failed discovery authorized an input device")
				return nil, nil
			}
			device, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
			if device != nil || err == nil || l.closes != 1 {
				t.Fatalf("failed discovery device=%T err=%v closes=%d", device, err, l.closes)
			}
			if tc.listErr != nil && !errors.Is(err, tc.listErr) {
				t.Fatalf("lost enumeration error %v", err)
			}
			if tc.closeErr != nil && !errors.Is(err, tc.closeErr) {
				t.Fatalf("lost teardown error %v", err)
			}
		})
	}
}
