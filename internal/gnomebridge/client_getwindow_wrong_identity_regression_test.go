//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"testing"
)

type mismatchedWindowReadObject struct{ returnedID string }

func (o mismatchedWindowReadObject) CallWithContext(_ context.Context, _ string, _ dbus.Flags, _ ...any) *dbus.Call {
	return &dbus.Call{Body: []any{WindowInfo{ID: o.returnedID, Title: "another private document"}}}
}

// A late or misrouted D-Bus reply must never expose metadata belonging to a
// different window. The requested ID is the caller's only authorization scope.
func TestBridgeGetWindowRejectsMismatchedOrMissingReplyIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		valid    bool
	}{
		{"different window", "18", false},
		{"missing id", "", false},
		{"whitespace id", "   ", false},
		{"matching identity", "17", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{obj: mismatchedWindowReadObject{returnedID: tc.id}, closeDone: make(chan struct{})}
			window, err := c.GetWindow(context.Background(), "17")
			if tc.valid {
				if err != nil || window.ID != "17" {
					t.Fatalf("correct identity rejected: %+v %v", window, err)
				}
			} else if !errors.Is(err, ErrObjectNotFound) || window.ID != "" || window.Title != "" {
				t.Fatalf("foreign window metadata leaked: %+v %v", window, err)
			}
		})
	}
}

func TestBridgeGetWindowMissingRequestIdentityNeverReachesProvider(t *testing.T) {
	object := &recordingObject{protocol: ProtocolVersion}
	c := &Client{obj: object, closeDone: make(chan struct{})}
	for _, id := range []string{"", " ", "\t"} {
		info, err := c.GetWindow(context.Background(), id)
		if !errors.Is(err, ErrObjectNotFound) || info != (WindowInfo{}) {
			t.Fatalf("missing request ID %q returned %+v %v", id, info, err)
		}
	}
	if len(object.calls) != 0 {
		t.Fatalf("invalid requests reached GNOME bridge: %+v", object.calls)
	}
}
