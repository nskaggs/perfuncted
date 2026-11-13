package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

type failWindowNthWrite struct {
	count   int
	failAt  int
	failure error
	sent    bytes.Buffer
}

func (w *failWindowNthWrite) Write(p []byte) (int, error) {
	w.count++
	if w.count == w.failAt {
		return 0, w.failure
	}
	return w.sent.Write(p)
}

// A partial window listing must not continue emitting subsequent windows when
// the output consumer disconnects. This also protects stream consumers from
// interpreting a truncated list as complete.
func TestWindowListPlainStopsOnFailedSecondWindowWrite(t *testing.T) {
	denied := errors.New("synthetic broken output pipe")
	w := &failWindowNthWrite{failAt: 2, failure: denied}
	wins := []window.Info{
		{NativeID: "one", Title: "Editor"},
		{NativeID: "two", Title: "Private Dialog"},
		{NativeID: "three", Title: "Should not be written"},
	}
	err := printWindowListPlain(w, wins)
	if !errors.Is(err, denied) || w.count != 2 || !strings.Contains(w.sent.String(), "Editor") || strings.Contains(w.sent.String(), "Private Dialog") || strings.Contains(w.sent.String(), "Should not be written") {
		t.Fatalf("failed window-list sink acknowledged: err=%v writes=%d out=%q", err, w.count, w.sent.String())
	}
}

// Once a session has opened, read failures must propagate without replacing
// the backend error with an empty list, missing-window result, or false title.
func TestWindowReadCommandsRejectBackendFailuresWithoutResults(t *testing.T) {
	failure := errors.New("synthetic compositor list unavailable")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"plain list", []string{"window", "list"}},
		{"JSON list", []string{"window", "list", "--output=json"}},
		{"active title", []string{"window", "active"}},
		{"activate by native id", []string{"window", "activate-id", "existing-id"}},
		{"close by native id", []string{"window", "close-id", "existing-id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return pftest.New(nil, nil, &pftest.Manager{Err: failure}, nil), nil
				}
			})
			if opens != 1 || code == 0 || out != "" || !strings.Contains(stderr, failure.Error()) {
				t.Fatalf("failed window read %v accepted: opens=%d code=%d out=%q stderr=%q", tc.args, opens, code, out, stderr)
			}
		})
	}
}

// The ID selector must reject an absent ID without consulting the compositor.
func TestFindWindowByStableIDRejectsEmptyIDBeforeListing(t *testing.T) {
	pf := pftest.New(nil, nil, &pftest.Manager{Err: errors.New("unexpected backend listing")}, nil)
	defer pf.Close()
	_, err := findWindowByStableID(context.Background(), pf.Windows, "")
	if !errors.Is(err, window.ErrInvalidArgument) || strings.Contains(err.Error(), "unexpected backend listing") {
		t.Fatalf("empty window ID queried backend: %v", err)
	}
}
