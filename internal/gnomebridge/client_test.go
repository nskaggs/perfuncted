//go:build linux
// +build linux

package gnomebridge

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

type recordingObject struct {
	mu       sync.Mutex
	protocol uint32
	calls    []recordedCall
}

type recordedCall struct {
	method string
	args   []any
}

func (o *recordingObject) CallWithContext(ctx context.Context, method string, _ dbus.Flags, args ...any) *dbus.Call {
	if err := ctx.Err(); err != nil {
		return &dbus.Call{Err: err}
	}
	o.mu.Lock()
	o.calls = append(o.calls, recordedCall{method: method, args: args})
	o.mu.Unlock()
	call := &dbus.Call{}
	switch method {
	case CoreInterface + ".GetProtocolVersion":
		call.Body = []any{o.protocol}
	case CoreInterface + ".GetExtensionVersion":
		call.Body = []any{"2"}
	case CoreInterface + ".GetShellVersion":
		call.Body = []any{"51.0"}
	case CoreInterface + ".GetCapabilities":
		call.Body = []any{[]string{"windows", "screen", "input", "clipboard"}}
	case WindowsInterface + ".ListWindows":
		call.Body = []any{[]WindowInfo{{ID: "17", Title: "Terminal", AppID: "org.gnome.Terminal", PID: 42, X: 1, Y: 2, Width: 800, Height: 600, Active: true}}}
	case WindowsInterface + ".GetWindow":
		call.Body = []any{WindowInfo{ID: "17", Title: "Terminal", Width: 800, Height: 600}}
	case WindowsInterface + ".GetActiveWindow":
		call.Body = []any{WindowInfo{ID: "17", Title: "Terminal", Active: true}}
	case InputInterface + ".PointerLocation":
		call.Body = []any{int32(11), int32(22)}
	}
	return call
}

func newRecordingClient(t *testing.T) (*Client, *recordingObject) {
	t.Helper()
	object := &recordingObject{protocol: ProtocolVersion}
	client, err := newClientWithObject(context.Background(), object)
	if err != nil {
		t.Fatalf("newClientWithObject: %v", err)
	}
	return client, object
}

func TestClientNegotiates(t *testing.T) {
	client, _ := newRecordingClient(t)

	if client.ProtocolVersion() != ProtocolVersion || client.ExtensionVersion() != "2" || client.ShellVersion() != "51.0" {
		t.Fatalf("negotiated metadata = protocol %d, extension %q, shell %q", client.ProtocolVersion(), client.ExtensionVersion(), client.ShellVersion())
	}
	caps := client.Capabilities()
	caps[0] = "changed"
	if !client.HasCapability(CapabilityScreen) || client.Capabilities()[0] == "changed" {
		t.Fatalf("Capabilities did not return a defensive copy: %v", client.Capabilities())
	}
}

func TestClientUsesTypedMethods(t *testing.T) {
	client, object := newRecordingClient(t)

	windows, err := client.ListWindows(context.Background())
	if err != nil || len(windows) != 1 || windows[0].ID != "17" {
		t.Fatalf("ListWindows = %#v, %v", windows, err)
	}
	window, err := client.GetWindow(context.Background(), "17")
	if err != nil || window.ID != "17" {
		t.Fatalf("GetWindow = %#v, %v", window, err)
	}
	if moveErr := client.Move(context.Background(), "17", 4, 5); moveErr != nil {
		t.Fatalf("Move: %v", moveErr)
	}
	x, y, err := client.PointerLocation(context.Background())
	if err != nil || x != 11 || y != 22 {
		t.Fatalf("PointerLocation = %d,%d,%v", x, y, err)
	}
	if err := client.SetText(context.Background(), "hello"); err != nil {
		t.Fatalf("SetText: %v", err)
	}
	if err := client.Paste(context.Background(), "é"); err != nil {
		t.Fatalf("Paste: %v", err)
	}

	assertRecordedMove(t, object)
}

func assertRecordedMove(t *testing.T, object *recordingObject) {
	t.Helper()
	object.mu.Lock()
	defer object.mu.Unlock()
	for _, call := range object.calls {
		if call.method == WindowsInterface+".Move" {
			if !reflect.DeepEqual(call.args, []any{"17", int32(4), int32(5)}) {
				t.Fatalf("Move call = %#v, want typed args", call)
			}
			return
		}
	}
	t.Fatal("Move call was not recorded")
}

func TestClientRejectsProtocolMismatch(t *testing.T) {
	object := &recordingObject{protocol: ProtocolVersion + 1}
	_, err := newClientWithObject(context.Background(), object)
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("newClientWithObject error = %v, want protocol mismatch", err)
	}
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("error = %v, want ErrProtocolMismatch", err)
	}
}

func TestDecodeWindowEvent(t *testing.T) {
	window := []any{"17", "Terminal", "org.gnome.Terminal", "Terminal", int32(42), int32(1), int32(2), int32(800), int32(600), true, false, true, false}
	tests := []struct {
		name string
		sig  *dbus.Signal
		want WindowEvent
	}{
		{
			name: "added",
			sig:  &dbus.Signal{Path: dbus.ObjectPath(ObjectPath), Name: WindowsInterface + ".WindowAdded", Body: []any{window}},
			want: WindowEvent{Kind: WindowAddedEvent, ID: "17", Window: WindowInfo{ID: "17", Title: "Terminal", AppID: "org.gnome.Terminal", Class: "Terminal", PID: 42, X: 1, Y: 2, Width: 800, Height: 600, Active: true, Maximized: true}},
		},
		{
			name: "removed",
			sig:  &dbus.Signal{Path: dbus.ObjectPath(ObjectPath), Name: WindowsInterface + ".WindowRemoved", Body: []any{"17"}},
			want: WindowEvent{Kind: WindowRemovedEvent, ID: "17"},
		},
		{
			name: "focus",
			sig:  &dbus.Signal{Path: dbus.ObjectPath(ObjectPath), Name: WindowsInterface + ".FocusChanged", Body: []any{"17"}},
			want: WindowEvent{Kind: FocusChangedEvent, ID: "17"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := decodeWindowEvent(test.sig)
			if !ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("decodeWindowEvent = %#v, %v; want %#v, true", got, ok, test.want)
			}
		})
	}
	if _, ok := decodeWindowEvent(&dbus.Signal{Path: dbus.ObjectPath("/wrong"), Name: WindowsInterface + ".WindowRemoved", Body: []any{"17"}}); ok {
		t.Fatal("decodeWindowEvent accepted a signal from the wrong path")
	}
}

func TestTranslateDBusError(t *testing.T) {
	for _, test := range []struct {
		name string
		want error
	}{
		{name: "org.freedesktop.DBus.Error.UnknownObject", want: ErrUnavailable},
		{name: "org.freedesktop.DBus.Error.ServiceUnknown", want: ErrUnavailable},
		{name: "io.github.nskaggs.perfuncted.Gnome1.Error.NotFound", want: ErrObjectNotFound},
		{name: "io.github.nskaggs.perfuncted.Gnome1.Error.Unsupported", want: ErrUnavailable},
	} {
		err := translateDBusError(dbus.NewError(test.name, []any{"test"}))
		if !errors.Is(err, test.want) {
			t.Errorf("translateDBusError(%q) = %v, want %v", test.name, err, test.want)
		}
	}
}

func TestClientCancellationAndClose(t *testing.T) {
	object := &recordingObject{protocol: ProtocolVersion}
	client, err := newClientWithObject(context.Background(), object)
	if err != nil {
		t.Fatalf("newClientWithObject: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Ping(canceled); err == nil {
		t.Fatal("Ping with canceled context succeeded")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := client.Ping(context.Background()); err == nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Ping after Close = %v, want ErrUnavailable", err)
	}
}
