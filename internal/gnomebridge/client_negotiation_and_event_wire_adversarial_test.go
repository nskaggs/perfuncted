//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// The extension metadata handshake is atomic. A malformed or failing
// intermediate reply must never publish partially negotiated capabilities.
type negotiationFaultObject struct {
	failureMethod   string
	malformedMethod string
	seen            []string
}

func (o *negotiationFaultObject) CallWithContext(_ context.Context, method string, _ dbus.Flags, _ ...any) *dbus.Call {
	o.seen = append(o.seen, method)
	if strings.HasSuffix(method, o.failureMethod) && o.failureMethod != "" {
		return &dbus.Call{Err: errors.New("synthetic negotiation failure")}
	}
	if strings.HasSuffix(method, o.malformedMethod) && o.malformedMethod != "" {
		return &dbus.Call{Body: []any{uint64(37)}}
	}
	switch method {
	case CoreInterface + ".GetProtocolVersion":
		return &dbus.Call{Body: []any{ProtocolVersion}}
	case CoreInterface + ".GetExtensionVersion":
		return &dbus.Call{Body: []any{"synthetic-extension"}}
	case CoreInterface + ".GetShellVersion":
		return &dbus.Call{Body: []any{"synthetic-shell"}}
	case CoreInterface + ".GetCapabilities":
		return &dbus.Call{Body: []any{[]string{"windows", "screen"}}}
	}
	return &dbus.Call{}
}
func TestBridgeNegotiationFailuresCannotPublishPartialMetadata(t *testing.T) {
	for _, stage := range []string{"GetProtocolVersion", "GetExtensionVersion", "GetShellVersion", "GetCapabilities"} {
		for _, fault := range []string{"error", "malformed"} {
			t.Run(stage+"/"+fault, func(t *testing.T) {
				assertNegotiationFaultDoesNotPublishMetadata(t, stage, fault)
			})
		}
	}
	var c *Client
	if c.ProtocolVersion() != 0 || c.ExtensionVersion() != "" || c.ShellVersion() != "" || c.Capabilities() != nil || c.HasCapability("windows") {
		t.Fatal("nil client exposed capability authority")
	}
}

func assertNegotiationFaultDoesNotPublishMetadata(t *testing.T, stage, fault string) {
	t.Helper()
	o := &negotiationFaultObject{}
	if fault == "error" {
		o.failureMethod = stage
	} else {
		o.malformedMethod = stage
	}
	c := &Client{obj: o, closeDone: make(chan struct{})}
	err := negotiate(context.Background(), c)
	if err == nil {
		t.Fatalf("%s %s response was accepted", stage, fault)
	}
	if c.ProtocolVersion() != 0 || c.ExtensionVersion() != "" || c.ShellVersion() != "" || len(c.Capabilities()) != 0 {
		t.Fatalf("partial negotiation exposed: version=%d ext=%s shell=%s caps=%v", c.ProtocolVersion(), c.ExtensionVersion(), c.ShellVersion(), c.Capabilities())
	}
	if !strings.HasSuffix(o.seen[len(o.seen)-1], stage) || len(o.seen) != map[string]int{"GetProtocolVersion": 1, "GetExtensionVersion": 2, "GetShellVersion": 3, "GetCapabilities": 4}[stage] {
		t.Fatalf("failure did not stop at the requested stage: %v", o.seen)
	}
}

func TestBridgeMalformedWindowSignalsCannotEnterLifecycleStream(t *testing.T) {
	valid := WindowInfo{ID: "17", Title: "Private report"}
	for _, tc := range []struct {
		name   string
		signal *dbus.Signal
	}{
		{"missing signal", nil},
		{"foreign path", &dbus.Signal{Name: WindowsInterface + ".WindowAdded", Path: "/foreign", Body: []any{valid}}},
		{"unknown member", &dbus.Signal{Name: WindowsInterface + ".NotAnEvent", Path: dbus.ObjectPath(ObjectPath), Body: []any{valid}}},
		{"missing removal", &dbus.Signal{Name: WindowsInterface + ".WindowRemoved", Path: dbus.ObjectPath(ObjectPath)}},
		{"extra removal arg", &dbus.Signal{Name: WindowsInterface + ".WindowRemoved", Path: dbus.ObjectPath(ObjectPath), Body: []any{"17", "18"}}},
		{"wrong removal arg", &dbus.Signal{Name: WindowsInterface + ".WindowRemoved", Path: dbus.ObjectPath(ObjectPath), Body: []any{int32(17)}}},
		{"missing focus", &dbus.Signal{Name: WindowsInterface + ".FocusChanged", Path: dbus.ObjectPath(ObjectPath)}},
		{"wrong focus arg", &dbus.Signal{Name: WindowsInterface + ".FocusChanged", Path: dbus.ObjectPath(ObjectPath), Body: []any{true}}},
		{"missing added window", &dbus.Signal{Name: WindowsInterface + ".WindowAdded", Path: dbus.ObjectPath(ObjectPath)}},
		{"extra changed window", &dbus.Signal{Name: WindowsInterface + ".WindowChanged", Path: dbus.ObjectPath(ObjectPath), Body: []any{valid, valid}}},
		{"wrong changed window", &dbus.Signal{Name: WindowsInterface + ".WindowChanged", Path: dbus.ObjectPath(ObjectPath), Body: []any{fmt.Sprintf("%d", 17)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, ok := decodeWindowEvent(tc.signal)
			if ok || !reflect.DeepEqual(event, WindowEvent{}) {
				t.Fatalf("malformed event accepted: %+v %v", event, ok)
			}
		})
	}
}

func TestBridgeTypedWindowReadErrorsDoNotReturnPartialWindowMetadata(t *testing.T) {
	failure := errors.New("synthetic D-Bus provider unavailable")
	for _, tc := range []struct {
		name, method string
		read         func(*Client) (any, error)
	}{
		{"window list", "ListWindows", func(c *Client) (any, error) { return c.ListWindows(context.Background()) }},
		{"active window", "GetActiveWindow", func(c *Client) (any, error) { return c.GetActiveWindow(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := &bridgeFailingObject{errorFor: "." + tc.method, failure: failure}
			c := &Client{obj: obj}
			value, err := tc.read(c)
			if !errors.Is(err, failure) {
				t.Fatalf("provider failure lost: %v", err)
			}
			switch returned := value.(type) {
			case []WindowInfo:
				if returned != nil {
					t.Fatalf("failed read exposed list: %+v", returned)
				}
			case WindowInfo:
				if returned.ID != "" {
					t.Fatalf("failed read exposed window: %+v", returned)
				}
			default:
				t.Fatalf("unexpected read result %T", value)
			}
		})
	}
}
