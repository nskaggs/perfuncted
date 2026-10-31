package window

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"strings"
	"testing"
)

func TestGnomeNativeManagerFailsClosedOnUninitializedAndDisconnectedBridge(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		manager *GnomeNativeManager
	}{
		{"missing bridge", &GnomeNativeManager{}},
		{"unreachable bridge", &GnomeNativeManager{bridge: &gnomebridge.Client{}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			m := fixture.manager
			methods := []struct {
				name   string
				invoke func() error
			}{
				{"activate", func() error { return m.ActivateByID(context.Background(), "17") }},
				{"move", func() error { return m.MoveByID(context.Background(), "17", 30, 40) }},
				{"resize", func() error { return m.ResizeByID(context.Background(), "17", 300, 200) }},
				{"close", func() error { return m.CloseWindowByID(context.Background(), "17") }},
				{"minimize", func() error { return m.MinimizeByID(context.Background(), "17") }},
				{"maximize", func() error { return m.MaximizeByID(context.Background(), "17") }},
				{"fullscreen", func() error { return m.FullscreenByID(context.Background(), "17") }},
				{"unfullscreen", func() error { return m.UnfullscreenByID(context.Background(), "17") }},
				{"restore", func() error { return m.RestoreByID(context.Background(), "17") }},
				{"sync", func() error { return m.Sync(context.Background()) }},
			}
			for _, tc := range methods {
				t.Run(tc.name, func(t *testing.T) {
					if err := tc.invoke(); err == nil {
						t.Fatal("unavailable GNOME service accepted a mutation")
					}
				})
			}
			if windows, err := m.List(context.Background()); err == nil || len(windows) != 0 {
				t.Fatalf("unavailable window listing: %+v %v", windows, err)
			}
			if title, err := m.ActiveTitle(context.Background()); err == nil || title != "" {
				t.Fatalf("unavailable active title: %q %v", title, err)
			}
			if info, err := m.InfoByID(context.Background(), "17"); err == nil || info.NativeID != "" {
				t.Fatalf("unavailable node info: %+v %v", info, err)
			}
			var count int
			for _, err := range m.IterateWindows(context.Background()) {
				count++
				if err == nil {
					t.Fatal("iterator acknowledged unavailable bus")
				}
			}
			if count != 1 {
				t.Fatalf("iterator did not surface a single transport error: %d", count)
			}
			if err := m.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
		})
	}
}
func TestGnomeNativeManagerRejectsMalformedGeometryBeforeTransport(t *testing.T) {
	m := &GnomeNativeManager{bridge: &gnomebridge.Client{}}
	for _, tc := range []struct {
		name   string
		invoke func() error
	}{
		{"negative width", func() error { return m.ResizeByID(context.Background(), "17", -1, 200) }},
		{"zero height", func() error { return m.ResizeByID(context.Background(), "17", 300, 0) }},
		{"overflow x", func() error { return m.MoveByID(context.Background(), "17", 1<<33, 20) }},
		{"overflow width", func() error { return m.ResizeByID(context.Background(), "17", 1<<33, 20) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.invoke()
			if err == nil || strings.Contains(err.Error(), "client is closed") {
				t.Fatalf("invalid geometry reached disconnected transport: %v", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.ActivateByID(canceled, "17"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled activation was not rejected: %v", err)
	}
}
