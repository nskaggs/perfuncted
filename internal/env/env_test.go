package env

import (
	"testing"
)

func TestMergeOverrides(t *testing.T) {
	t.Parallel()
	base := []string{"A=1", "B=2", "C=3"}
	over := []string{"B=20", "D=4"}
	got := Merge(base, over...)
	// Expect A=1, C=3 preserved, B overridden to 20, D appended
	m := make(map[string]string)
	for _, kv := range got {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	if m["A"] != "1" {
		t.Fatalf("A = %q, want 1", m["A"])
	}
	if m["B"] != "20" {
		t.Fatalf("B = %q, want 20", m["B"])
	}
	if m["C"] != "3" {
		t.Fatalf("C = %q, want 3", m["C"])
	}
	if m["D"] != "4" {
		t.Fatalf("D = %q, want 4", m["D"])
	}
}

func TestMergeClears(t *testing.T) {
	t.Parallel()
	base := []string{"X=old", "Y=keep"}
	over := []string{"X="}
	got := Merge(base, over...)
	m := make(map[string]string)
	for _, kv := range got {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	if _, ok := m["X"]; !ok {
		t.Fatalf("X missing after merge")
	}
	if m["X"] != "" {
		t.Fatalf("X = %q, want empty", m["X"])
	}
	if m["Y"] != "keep" {
		t.Fatalf("Y = %q, want keep", m["Y"])
	}
}

func TestMergeEmptyBase(t *testing.T) {
	t.Parallel()
	got := Merge(nil, "A=1", "B=2")
	m := envMap(got)
	if m["A"] != "1" || m["B"] != "2" {
		t.Fatalf("Merge(nil) = %v", got)
	}
}

func TestMergeLastOverrideWins(t *testing.T) {
	t.Parallel()
	got := Merge([]string{"A=base"}, "A=first", "A=second")
	if m := envMap(got); m["A"] != "second" {
		t.Fatalf("A = %q, want second", m["A"])
	}
}

func TestMergeOverlayReplacesKeyOnlyBaseEntry(t *testing.T) {
	t.Parallel()

	got := Merge([]string{"FOO", "BAR=1"}, "FOO=2")
	want := []string{"BAR=1", "FOO=2"}
	if len(got) != len(want) {
		t.Fatalf("Merge returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Merge[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestMergePreservesKeyOnlyBaseEntryWithoutOverlay(t *testing.T) {
	t.Parallel()

	got := Merge([]string{"FOO", "BAR=1"})
	want := []string{"FOO", "BAR=1"}
	if len(got) != len(want) {
		t.Fatalf("Merge returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Merge[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func envMap(kvs []string) map[string]string {
	m := make(map[string]string)
	for _, kv := range kvs {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}

func TestRuntimeLookupAndHas(t *testing.T) {
	t.Parallel()

	rt := FromEnviron([]string{"FOO=bar", "EMPTY="})
	if !rt.Has("FOO") {
		t.Fatalf("expected rt.Has(FOO) to be true")
	}
	if val, ok := rt.Lookup("FOO"); !ok || val != "bar" {
		t.Fatalf("Lookup(FOO) = (%q, %v), want (\"bar\", true)", val, ok)
	}
	if !rt.Has("EMPTY") {
		t.Fatalf("expected rt.Has(EMPTY) to be true")
	}
	if val, ok := rt.Lookup("EMPTY"); !ok || val != "" {
		t.Fatalf("Lookup(EMPTY) = (%q, %v), want (\"\", true)", val, ok)
	}
	if rt.Has("UNSET") {
		t.Fatalf("expected rt.Has(UNSET) to be false")
	}
	if val, ok := rt.Lookup("UNSET"); ok || val != "" {
		t.Fatalf("Lookup(UNSET) = (%q, %v), want (\"\", false)", val, ok)
	}

	var zero Runtime
	if zero.Has("FOO") {
		t.Fatalf("expected zero.Has(FOO) to be false")
	}
	if val, ok := zero.Lookup("FOO"); ok || val != "" {
		t.Fatalf("zero.Lookup(FOO) = (%q, %v), want (\"\", false)", val, ok)
	}
}

func TestWithSessionDoesNotInheritHostAccessibilityBus(t *testing.T) {
	t.Parallel()

	rt := FromEnviron([]string{
		"AT_SPI_BUS=unix:path=/host/accessibility-bus",
		"ATSPI_BUS_ADDRESS=unix:path=/host/canonical-accessibility-bus",
		"AT_SPI_BUS_ADDRESS=unix:path=/host/legacy-accessibility-bus",
		"GTK_MODULES=host-bridge",
		"GTK_A11Y=1",
		"GNOME_ACCESSIBILITY=1",
		"QT_ACCESSIBILITY=1",
		"QT_LINUX_ACCESSIBILITY_ALWAYS_ON=1",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/host/session-bus",
	})
	managed := rt.WithSession(
		"/tmp/perfuncted-xdg",
		"wayland-1",
		"unix:path=/tmp/perfuncted-xdg/bus",
	)
	// WithSession returns the snapshot unchanged.
	for _, key := range []string{"AT_SPI_BUS", "ATSPI_BUS_ADDRESS", "AT_SPI_BUS_ADDRESS", "GTK_MODULES", "GTK_A11Y", "GNOME_ACCESSIBILITY", "QT_ACCESSIBILITY", "QT_LINUX_ACCESSIBILITY_ALWAYS_ON", "DBUS_SESSION_BUS_ADDRESS"} {
		if got, ok := managed.Lookup(key); !ok {
			t.Fatalf("%s missing after WithSession, want preserved", key)
		} else if got == "" {
			t.Fatalf("%s = %q, want preserved value", key, got)
		}
	}
}

func TestWithAccessibilityBusPublishesManagedAddress(t *testing.T) {
	t.Parallel()

	managed := FromEnviron([]string{
		"AT_SPI_BUS=host-x-root-property",
		"AT_SPI_BUS_ADDRESS=host-legacy-address",
	}).WithAccessibilityBus(" unix:path=/tmp/perfuncted-a11y ")
	// WithAccessibilityBus returns the snapshot unchanged.
	if got := managed.Get("AT_SPI_BUS_ADDRESS"); got != "host-legacy-address" {
		t.Fatalf("AT_SPI_BUS_ADDRESS = %q, want preserved host value", got)
	}
	empty := managed.WithAccessibilityBus("")
	if got, ok := empty.Lookup("AT_SPI_BUS"); !ok {
		t.Fatalf("AT_SPI_BUS missing after WithAccessibilityBus, want preserved")
	} else if got != "host-x-root-property" {
		t.Fatalf("AT_SPI_BUS = %q, want preserved", got)
	}
}
