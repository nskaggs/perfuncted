//go:build linux
// +build linux

package gnomebridge

import (
	"context"
	"encoding/json"
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

func TestParseGSettingsBool(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "true", want: true},
		{value: "@b true", want: true},
		{value: "false", want: false},
		{value: "@b false", want: false},
		{value: "['true']", want: false},
	} {
		if got := parseGSettingsBool(test.value); got != test.want {
			t.Fatalf("parseGSettingsBool(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestInstallerWritesBundledExtensionAndPreservesEnabledList(t *testing.T) {
	dataHome := t.TempDir()
	createPreviousExtension(t, dataHome)
	var calls [][]string
	runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "get" {
			switch args[2] {
			case "allow-extension-installation":
				return []byte("true"), nil
			case "disable-user-extensions":
				return []byte("false"), nil
			case "disabled-extensions":
				return []byte("[]"), nil
			case "enabled-extensions":
				return []byte("['other@example']"), nil
			}
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
	for _, name := range []string{"metadata.json", "extension.js", "service.js", "windows.js", "screen.js", "input.js", "clipboard.js", "errors.js"} {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			t.Fatalf("installed %s: %v", name, err)
		}
	}
	assertPreviousExtensionReplaced(t, path)
	if len(calls) != 5 || calls[4][0] != "set" {
		t.Fatalf("gsettings calls = %v, want policy/list gets and enabled set", calls)
	}
	if !strings.Contains(calls[4][3], "other@example") || !strings.Contains(calls[4][3], extensionUUID) {
		t.Fatalf("enabled-extensions value = %q, want both extensions", calls[4][3])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), extensionUUID+".old")); !os.IsNotExist(err) {
		t.Fatalf("old extension backup remains: %v", err)
	}
}

func createPreviousExtension(t *testing.T, dataHome string) {
	t.Helper()
	destination := filepath.Join(dataHome, extensionDirectory)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatalf("create previous extension directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "stale.js"), []byte("old extension"), 0o644); err != nil {
		t.Fatalf("write previous extension marker: %v", err)
	}
}

func assertPreviousExtensionReplaced(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(path, "stale.js")); !os.IsNotExist(err) {
		t.Fatalf("previous extension contents remain after replacement: %v", err)
	}
}

func TestInstallerDoesNotRewriteEnabledSettingWhenAlreadyEnabled(t *testing.T) {
	var calls [][]string
	runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] != "get" {
			return nil, nil
		}
		switch args[2] {
		case "allow-extension-installation":
			return []byte("true"), nil
		case "disable-user-extensions", "disabled-extensions":
			return []byte("false"), nil
		default:
			return []byte("['" + extensionUUID + "']"), nil
		}
	}
	installer := Installer{DataHome: t.TempDir(), Run: runner}
	if _, err := installer.Install(context.Background(), env.FromEnviron(nil)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(calls) != 4 || calls[3][0] != "get" {
		t.Fatalf("gsettings calls = %v, want policy/list gets", calls)
	}
}

func TestInstallerClearsOnlyOwnDisabledExtension(t *testing.T) {
	var calls [][]string
	runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] != "get" {
			return nil, nil
		}
		switch args[2] {
		case "allow-extension-installation":
			return []byte("true"), nil
		case "disable-user-extensions":
			return []byte("false"), nil
		case "disabled-extensions":
			return []byte("['other@example', '" + extensionUUID + "']"), nil
		default:
			return []byte("['" + extensionUUID + "']"), nil
		}
	}
	installer := Installer{DataHome: t.TempDir(), Run: runner}
	if _, err := installer.Install(context.Background(), env.FromEnviron(nil)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(calls) != 5 || calls[3][0] != "set" || calls[3][3] != "['other@example']" {
		t.Fatalf("gsettings calls = %v, want only own disabled entry removed", calls)
	}
}

func TestInstallerRejectsDisabledUserExtensions(t *testing.T) {
	var calls [][]string
	runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[2] == "allow-extension-installation" {
			return []byte("true"), nil
		}
		return []byte("true"), nil
	}
	installer := Installer{DataHome: t.TempDir(), Run: runner}
	_, err := installer.Install(context.Background(), env.FromEnviron(nil))
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "disable-user-extensions") {
		t.Fatalf("Install error = %v, want actionable policy error", err)
	}
	if len(calls) != 2 {
		t.Fatalf("gsettings calls = %v, want installation and user-extension policy checks", calls)
	}
}

func TestInstallerRejectsDisallowedExtensionInstallation(t *testing.T) {
	var calls [][]string
	runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return []byte("false"), nil
	}
	installer := Installer{DataHome: t.TempDir(), Run: runner}
	_, err := installer.Install(context.Background(), env.FromEnviron(nil))
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "allow-extension-installation") {
		t.Fatalf("Install error = %v, want actionable installation-policy error", err)
	}
	if len(calls) != 1 {
		t.Fatalf("gsettings calls = %v, want installation policy check only", calls)
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
		{name: "older", running: "0", bundled: ExtensionVersion, want: true},
		{name: "same", running: ExtensionVersion, bundled: ExtensionVersion, want: false},
		{name: "newer", running: "2", bundled: ExtensionVersion, want: false},
		{name: "empty", running: "", bundled: ExtensionVersion, want: false},
		{name: "development", running: "dev", bundled: ExtensionVersion, want: false},
		{name: "invalid bundled", running: "0", bundled: "dev", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extensionVersionNeedsUpdate(test.running, test.bundled); got != test.want {
				t.Fatalf("extensionVersionNeedsUpdate(%q, %q) = %v, want %v", test.running, test.bundled, got, test.want)
			}
		})
	}
}

func TestAtomicReplaceDirectoryPublishesCompleteExtensionTree(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(root, ".perfuncted-extension-stage")
	dest := filepath.Join(root, extensionUUID)
	for _, directory := range []string{tmp, dest} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	for name, value := range map[string]string{
		filepath.Join(tmp, "metadata.json"):  "new metadata",
		filepath.Join(tmp, "extension.js"):   "new extension",
		filepath.Join(dest, "metadata.json"): "old metadata",
		filepath.Join(dest, "extension.js"):  "old extension",
	} {
		if err := os.WriteFile(name, []byte(value), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := atomicReplaceDirectory(tmp, dest); err != nil {
		t.Fatalf("atomicReplaceDirectory: %v", err)
	}
	for name, want := range map[string]string{"metadata.json": "new metadata", "extension.js": "new extension"} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(got) != want {
			t.Fatalf("published %s = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("previous directory remains at staging path: %v", err)
	}
}

func TestConnectRuntimeUsesCurrentBridgeWithoutReinstalling(t *testing.T) {
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/session"})
	client := &Client{extensionVer: ExtensionVersion}
	installCalls := 0
	got, err := connectRuntime(context.Background(), rt,
		func(_ context.Context, address string) (*Client, error) {
			if address != rt.Get("DBUS_SESSION_BUS_ADDRESS") {
				t.Fatalf("bridge address = %q, want runtime address", address)
			}
			return client, nil
		},
		func(context.Context, env.Runtime) (string, error) {
			installCalls++
			return "", nil
		},
	)
	if err != nil || got != client {
		t.Fatalf("connectRuntime = %p, %v; want current client %p", got, err, client)
	}
	if installCalls != 0 {
		t.Fatalf("installer calls = %d, want zero for a current bridge", installCalls)
	}
}

func TestConnectRuntimeRefreshesObsoleteBridgeAndRequiresRestart(t *testing.T) {
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/session"})
	client := &Client{extensionVer: "0"}
	installCalls := 0
	got, err := connectRuntime(context.Background(), rt,
		func(context.Context, string) (*Client, error) { return client, nil },
		func(_ context.Context, gotRuntime env.Runtime) (string, error) {
			installCalls++
			if gotRuntime.Get("DBUS_SESSION_BUS_ADDRESS") != rt.Get("DBUS_SESSION_BUS_ADDRESS") {
				t.Fatalf("installer received runtime %v, want target runtime", gotRuntime)
			}
			return "/user-data/gnome-shell/extensions/" + extensionUUID, nil
		},
	)
	if got != nil || !errors.Is(err, ErrSessionRestartRequired) {
		t.Fatalf("connectRuntime = %p, %v; want restart-required error", got, err)
	}
	var restart *SessionRestartRequiredError
	if !errors.As(err, &restart) || restart.Path == "" {
		t.Fatalf("connectRuntime error = %v, want installed extension path", err)
	}
	if installCalls != 1 || !client.closed {
		t.Fatalf("installer calls = %d, client closed = %t; want one install and closed obsolete client", installCalls, client.closed)
	}
}

func TestConnectRuntimeInstallsForLiveBusWhenBridgeIsAbsent(t *testing.T) {
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/session"})
	installCalls := 0
	got, err := connectRuntime(context.Background(), rt,
		func(context.Context, string) (*Client, error) { return nil, ErrUnavailable },
		func(_ context.Context, gotRuntime env.Runtime) (string, error) {
			installCalls++
			if gotRuntime.Get("DBUS_SESSION_BUS_ADDRESS") == "" {
				t.Fatal("installer received runtime without a live session bus")
			}
			return "/user-data/gnome-shell/extensions/" + extensionUUID, nil
		},
	)
	if got != nil || !errors.Is(err, ErrSessionRestartRequired) {
		t.Fatalf("connectRuntime = %p, %v; want restart-required error", got, err)
	}
	var restart *SessionRestartRequiredError
	if !errors.As(err, &restart) || restart.Path == "" || installCalls != 1 {
		t.Fatalf("connectRuntime error = %v, installer calls = %d; want one install and typed path", err, installCalls)
	}
}

func TestConnectRuntimeKeepsProtocolMismatchExplicitAfterRefresh(t *testing.T) {
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/session"})
	protocolErr := &ProtocolError{Expected: ProtocolVersion, Actual: ProtocolVersion + 1}
	_, err := connectRuntime(context.Background(), rt,
		func(context.Context, string) (*Client, error) { return nil, protocolErr },
		func(context.Context, env.Runtime) (string, error) { return "/extension", nil },
	)
	if !errors.Is(err, ErrProtocolMismatch) || !errors.Is(err, ErrSessionRestartRequired) {
		t.Fatalf("connectRuntime error = %v, want explicit protocol mismatch and restart requirement", err)
	}
}

func TestConnectForCapabilityRejectsUnadvertisedCapability(t *testing.T) {
	client := &Client{caps: []string{CapabilityWindows}}
	got, err := connectForCapability(context.Background(), env.Runtime{}, CapabilityScreen,
		func(context.Context, env.Runtime) (*Client, error) { return client, nil },
	)
	if got != nil || err == nil || !strings.Contains(err.Error(), CapabilityScreen) {
		t.Fatalf("connectForCapability = %p, %v; want explicit unsupported capability error", got, err)
	}
	if !client.closed {
		t.Fatal("client remained open after rejecting unadvertised capability")
	}
}

func TestEmbeddedBridgeExportsInterfacesAndResolvesUnixFDHandles(t *testing.T) {
	errorText := embeddedAssetText(t, "errors.js")
	assertEmbeddedFragmentsPresent(t, errorText, "errors.js is missing the bridge error contract",
		"export function bridgeError(kind, message)",
		"io.github.nskaggs.perfuncted.Gnome1.Error.",
	)
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
		`<arg name="pixel_width" type="i" direction="out"/>`,
		`<arg name="scale" type="d" direction="out"/>`,
		"Text(text) { return this._require(this._input, 'input').text(text); }",
		"Paste(text)",
		"if (this._input && this._clipboard)",
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
		"new Mtk.Rectangle",
		"get_capture_final_size",
		"get_screen_width",
		"captureFull(handle, fdList)",
		"const metadata = captureRect(",
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
		"text(text)",
		"codepoint < 0x20 || codepoint > 0x7e",
		"pasteText(text, clipboard)",
		"clipboard.setText(text)",
		"const control = 0xffe3",
		"const v = 0x76",
		"notify_discrete_scroll",
		"throw bridgeError('Unsupported', 'GNOME pointer location is unavailable')",
	)
	assertEmbeddedFragmentsAbsent(t, inputText, "input.js must not use layout-dependent or continuous input",
		"unicode_to_keysym", "notify_scroll_continuous", "text(text, clipboard)")
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

func TestBundledExtensionDeclaresSupportedShellRange(t *testing.T) {
	raw, err := fs.ReadFile(extensionAssets, "assets/"+extensionUUID+"/metadata.json")
	if err != nil {
		t.Fatalf("read metadata.json: %v", err)
	}
	var meta struct {
		ShellVersion []string `json:"shell-version"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("parse metadata.json: %v", err)
	}
	want := []string{"46", "47", "48", "49", "50", "51"}
	if !slices.Equal(meta.ShellVersion, want) {
		t.Fatalf("shell-version = %v, want %v", meta.ShellVersion, want)
	}
}
