//go:build linux

package window

import (
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
)

type deniedKWinIntrospection struct{}

func (deniedKWinIntrospection) Introspect() (string, *dbus.Error) {
	return "", dbus.MakeFailedError(errors.New("synthetic private scripting introspection denied"))
}

// A KWin scripting object registered on the bus is not evidence that it
// permits the introspection needed to establish scripting capability.
// A failed private D-Bus read must never advertise window automation.
func TestKWinProbeRejectsFailedPrivateScriptingIntrospection(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	if err := provider.conn.Export(deniedKWinIntrospection{}, kwinScriptPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		t.Fatal(err)
	}
	runtime := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + provider.busAddress})
	result := checkKWinScript(runtime, compositor.KDE)
	if result.Available || result.Name != "kwin-scripting" || !strings.Contains(result.Reason, "not accessible") || !strings.Contains(result.Reason, "synthetic private scripting introspection denied") {
		t.Fatalf("KWin probe authorized failed introspection: %+v", result)
	}
}
