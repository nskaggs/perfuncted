//go:build linux

package window

import (
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
)

// Probe the actual D-Bus introspection surface, rather than granting KWin
// capability based solely on the caller's desktop environment label.
func TestKWinProbeRequiresIntrospectedScriptingInterface(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + provider.busAddress})
	result := checkKWinScript(rt, compositor.KDE)
	if result.Name != "kwin-scripting" || !result.Available || !strings.Contains(result.Reason, "accessible") {
		t.Fatalf("private-bus KWin scripting capability denied: %+v", result)
	}
	if unrelated := checkKWinScript(rt, compositor.GNOME); unrelated.Available || !strings.Contains(unrelated.Reason, "not a KDE") {
		t.Fatalf("KWin capability leaked into another compositor: %+v", unrelated)
	}
}

type syntheticAbsentKWinIntrospection struct{}

func (syntheticAbsentKWinIntrospection) Introspect() (string, *dbus.Error) {
	return `<node><interface name="org.example.Unrelated"/></node>`, nil
}

func TestKWinProbeRejectsPrivateBusWithoutScriptingInterface(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	if err := provider.conn.Export(syntheticAbsentKWinIntrospection{}, kwinScriptPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		t.Fatal(err)
	}
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + provider.busAddress})
	result := checkKWinScript(rt, compositor.KDE)
	if result.Available || result.Name != "kwin-scripting" || !strings.Contains(result.Reason, "interface absent") {
		t.Fatalf("KWin falsely advertised without scripting interface: %+v", result)
	}
}
