//go:build linux
// +build linux

package gnomebridge

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestParseEnabledExtensions(t *testing.T) {
	got := parseEnabledExtensions("@as ['one@example', 'two@example']")
	want := []string{"one@example", "two@example"}
	if !slices.Equal(got, want) {
		t.Fatalf("parseEnabledExtensions = %v, want %v", got, want)
	}
	if got := parseEnabledExtensions("[]"); len(got) != 0 {
		t.Fatalf("parseEnabledExtensions([]) = %v", got)
	}
}

func TestInstallerWritesBundledExtensionAndPreservesEnabledList(t *testing.T) {
	dataHome := t.TempDir()
	var calls [][]string
	runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "get" {
			return []byte("['other@example']"), nil
		}
		return nil, nil
	}
	installer := Installer{DataHome: dataHome, Run: runner}
	path, err := installer.Install(context.Background(), env.FromEnviron(nil))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if want := filepath.Join(dataHome, extensionDirectory); path != want {
		t.Fatalf("Install path = %q, want %q", path, want)
	}
	for _, name := range []string{"metadata.json", "extension.js", "service.js", "windows.js", "screen.js", "input.js", "clipboard.js"} {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			t.Fatalf("installed %s: %v", name, err)
		}
	}
	if len(calls) != 2 || calls[1][0] != "set" {
		t.Fatalf("gsettings calls = %v, want get and set", calls)
	}
	if !strings.Contains(calls[1][3], "other@example") || !strings.Contains(calls[1][3], extensionUUID) {
		t.Fatalf("enabled-extensions value = %q, want both extensions", calls[1][3])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), extensionUUID+".old")); !os.IsNotExist(err) {
		t.Fatalf("old extension backup remains: %v", err)
	}
}

func TestInstallerDoesNotRewriteEnabledSettingWhenAlreadyEnabled(t *testing.T) {
	var calls [][]string
	runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return []byte("['" + extensionUUID + "']"), nil
	}
	installer := Installer{DataHome: t.TempDir(), Run: runner}
	if _, err := installer.Install(context.Background(), env.FromEnviron(nil)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(calls) != 1 || calls[0][0] != "get" {
		t.Fatalf("gsettings calls = %v, want only get", calls)
	}
}

func TestInstallerRejectsFlatpakHostProvisioning(t *testing.T) {
	_, err := NewInstallerForRuntime(env.FromEnviron([]string{
		"FLATPAK_ID=io.github.nskaggs.perfuncted",
		"HOME=/home/user",
		"XDG_DATA_HOME=/home/user/.var/app/io.github.nskaggs.perfuncted/data",
	}))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("NewInstallerForRuntime error = %v, want ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "native perfuncted package") {
		t.Fatalf("NewInstallerForRuntime error = %v, want actionable Flatpak message", err)
	}
}

func TestExtensionVersionNeedsUpdateIsMonotonic(t *testing.T) {
	tests := []struct {
		name    string
		running string
		bundled string
		want    bool
	}{
		{name: "older", running: "1", bundled: "2", want: true},
		{name: "same", running: "2", bundled: "2", want: false},
		{name: "newer", running: "3", bundled: "2", want: false},
		{name: "empty", running: "", bundled: "2", want: false},
		{name: "development", running: "dev", bundled: "2", want: false},
		{name: "invalid bundled", running: "1", bundled: "dev", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extensionVersionNeedsUpdate(test.running, test.bundled); got != test.want {
				t.Fatalf("extensionVersionNeedsUpdate(%q, %q) = %v, want %v", test.running, test.bundled, got, test.want)
			}
		})
	}
}

func TestEmbeddedBridgeExportsInterfacesAndResolvesUnixFDHandles(t *testing.T) {
	serviceText := embeddedAssetText(t, "service.js")
	if got := strings.Count(serviceText, `<interface name="io.github.nskaggs.perfuncted.Gnome1.`); got != 5 {
		t.Fatalf("bridge interface XML count = %d, want 5", got)
	}
	if got := strings.Count(serviceText, "wrapJSObject(xml, this)"); got != 1 {
		t.Fatalf("service export loop count = %d, want one loop", got)
	}
	for _, fragment := range []string{
		"CORE_XML", "WINDOWS_XML", "SCREEN_XML", "INPUT_XML", "CLIPBOARD_XML",
		"const EXTENSION_VERSION = '" + ExtensionVersion + "';",
		"text, this._require(this._clipboard, 'clipboard'))",
	} {
		if !strings.Contains(serviceText, fragment) {
			t.Errorf("service.js is missing expected fragment %q", fragment)
		}
	}
	if !strings.Contains(serviceText, "this._windowsObject.emit_signal") {
		t.Error("window signals are not emitted on the Windows interface object")
	}
	assertEmbeddedFragmentsAbsent(t, serviceText, "GNOME bridge must not advertise a fake input synchronization barrier",
		`<method name="Sync"/>`, "Sync()")
	for _, fragment := range []string{
		"CaptureFull(fd, fdList) { return this._require(this._screen, 'screen').captureFull(fd, fdList); }",
		"return this._require(this._screen, 'screen').captureRegion(fd, x, y, width, height, fdList);",
	} {
		if !strings.Contains(serviceText, fragment) {
			t.Errorf("service.js does not propagate screenshot Promise in %q", fragment)
		}
	}

	screenText := embeddedAssetText(t, "screen.js")
	for _, fragment := range []string{
		"fdList.get_length()",
		"fdList.get(index)",
		"close_fd: true",
		"captureFull(handle, fdList)",
		"return new Promise",
		"return runScreenshot(",
	} {
		if !strings.Contains(screenText, fragment) {
			t.Errorf("screen.js is missing Unix FD handling fragment %q", fragment)
		}
	}
	if strings.Contains(screenText, "close_fd: false") {
		t.Error("screen.js must own and close the duplicated Unix FD")
	}
	if strings.Contains(embeddedAssetText(t, "clipboard.js"), "GLib.MainLoop") {
		t.Error("clipboard.js must not re-enter Shell with a nested main loop")
	}
	inputText := embeddedAssetText(t, "input.js")
	assertEmbeddedFragmentsPresent(t, inputText, "input.js is missing expected text/scroll fragment",
		"clipboard.setText(text)",
		"const control = 0xffe3",
		"const v = 0x76",
		"notify_discrete_scroll",
	)
	assertEmbeddedFragmentsAbsent(t, inputText, "input.js must not use layout-dependent or continuous input",
		"unicode_to_keysym", "notify_scroll_continuous")
}

func embeddedAssetText(t *testing.T, name string) string {
	t.Helper()
	asset, err := fs.ReadFile(extensionAssets, "assets/"+extensionUUID+"/"+name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(asset)
}

func assertEmbeddedFragmentsPresent(t *testing.T, text, message string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Errorf("%s %q", message, fragment)
		}
	}
}

func assertEmbeddedFragmentsAbsent(t *testing.T, text, message string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			t.Errorf("%s: found %q", message, fragment)
		}
	}
}
