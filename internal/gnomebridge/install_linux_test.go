//go:build linux

package gnomebridge

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Refuse to replace a non-directory GNOME extension target. A failed
// directory exchange must preserve both staged content and existing files.
func TestAtomicReplaceDirectoryRejectsExistingRegularFileWithoutMutation(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	target := filepath.Join(root, extensionUUID)
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "metadata.json"), []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing regular file"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := atomicReplaceDirectory(stage, target)
	if err == nil || !strings.Contains(err.Error(), "existing extension target is not a directory") {
		t.Fatalf("accepted non-directory extension target: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "existing regular file" {
		t.Fatalf("existing extension target mutated: %q %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(stage, "metadata.json"))
	if err != nil || string(got) != "staged" {
		t.Fatalf("staged extension mutated: %q %v", got, err)
	}
}

// An existing symlink must not be swapped away or followed during replacement.
func TestAtomicReplaceDirectoryPreservesExistingSymlinkAndTarget(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	external := filepath.Join(root, "external")
	dest := filepath.Join(root, extensionUUID)
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "sentinel"), []byte("unaltered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, dest); err != nil {
		t.Fatal(err)
	}
	if err := atomicReplaceDirectory(stage, dest); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("accepted symlink target: %v", err)
	}
	if got, err := os.Readlink(dest); err != nil || got != external {
		t.Fatalf("destination symlink mutated: %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(external, "sentinel")); err != nil || string(got) != "unaltered" {
		t.Fatalf("symlink destination contents mutated: %q %v", got, err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("staging directory lost on rejected symlink: %v", err)
	}
}

// Capability admission is based on the negotiated bridge's advertised
// abilities, not merely its presence. An authorized connection remains owned
// by the caller, while transport failure cannot invent a live client.
func TestBridgeCapabilityConnectionAdmitsOnlyAdvertisedLiveClient(t *testing.T) {
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/fixture"})
	connected := &Client{caps: []string{CapabilityWindows}}
	calls := 0
	got, err := connectForCapability(context.Background(), rt, CapabilityWindows,
		func(ctx context.Context, observed env.Runtime) (*Client, error) {
			calls++
			if ctx.Err() != nil || observed.Get("DBUS_SESSION_BUS_ADDRESS") != "unix:path=/fixture" {
				t.Fatalf("wrong admission context or runtime: %v %+v", ctx.Err(), observed)
			}
			return connected, nil
		},
	)
	if err != nil || got != connected || connected.closed || calls != 1 {
		t.Fatalf("authorized bridge was lost: got=%p err=%v closed=%v calls=%d", got, err, connected.closed, calls)
	}
	if err := got.Close(); err != nil {
		t.Fatalf("caller unable to close admitted connection: %v", err)
	}
	refused := errors.New("synthetic failed private D-Bus negotiation")
	got, err = connectForCapability(context.Background(), rt, CapabilityWindows,
		func(context.Context, env.Runtime) (*Client, error) { return nil, refused },
	)
	if got != nil || !errors.Is(err, refused) {
		t.Fatalf("failed negotiation authorized a connection: got=%p err=%v", got, err)
	}
}

// A GNOME bridge cannot authorize current-session automation when extension
// refresh failed, the session bus was absent, or an unavailable bridge could
// not be installed. Preserve the original failures and close stale clients.
func TestConnectRuntimeFailClosedRefreshAndInstallBoundaries(t *testing.T) {
	unavailable := errors.New("synthetic extension installation denied")
	bus := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/private-bus"})
	obsolete := &Client{extensionVer: "0"}
	installCount := 0
	got, err := connectRuntime(nil, bus,
		func(ctx context.Context, address string) (*Client, error) {
			if ctx == nil || address != "unix:path=/private-bus" {
				t.Fatalf("nil fallback context or wrong address: %v %q", ctx, address)
			}
			return obsolete, nil
		},
		func(context.Context, env.Runtime) (string, error) { installCount++; return "", unavailable },
	)
	if got != nil || !errors.Is(err, ErrUnavailable) || !errors.Is(err, unavailable) || !obsolete.closed || installCount != 1 {
		t.Fatalf("obsolete bridge refresh admitted: client=%v err=%v closed=%v installs=%d", got, err, obsolete.closed, installCount)
	}
	t.Run("no session bus", func(t *testing.T) {
		installs := 0
		got, err := connectRuntime(context.Background(), env.FromEnviron(nil),
			func(context.Context, string) (*Client, error) { return nil, ErrUnavailable },
			func(context.Context, env.Runtime) (string, error) { installs++; return "", nil },
		)
		if got != nil || !errors.Is(err, ErrUnavailable) || installs != 0 {
			t.Fatalf("bridge installed without bus: client=%v err=%v installs=%d", got, err, installs)
		}
	})
	t.Run("installation refused", func(t *testing.T) {
		installs := 0
		got, err := connectRuntime(context.Background(), bus,
			func(context.Context, string) (*Client, error) { return nil, ErrUnavailable },
			func(context.Context, env.Runtime) (string, error) { installs++; return "", unavailable },
		)
		if got != nil || !errors.Is(err, ErrUnavailable) || !errors.Is(err, unavailable) || installs != 1 {
			t.Fatalf("failed install authorized bridge: client=%v err=%v installs=%d", got, err, installs)
		}
	})
}

// Production installation must run gsettings from the selected runtime,
// not the user's ambient desktop. Exercise the default runner against an
// isolated executable and user-data tree, never the host's GNOME settings.
func TestInstallRuntimeUsesIsolatedDefaultGSettingsRunner(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bin, "gsettings")
	commands := filepath.Join(root, "commands.log")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PF_TEST_COMMAND_LOG\"\nif [ \"$1\" = get ]; then\n case \"$3\" in\n allow-extension-installation) printf 'true\\n';;\n disable-user-extensions) printf 'false\\n';;\n disabled-extensions) printf '[]\\n';;\n enabled-extensions) printf \"['other@example']\\n\";;\n *) exit 4;;\n esac\nfi\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dataHome := filepath.Join(root, "isolated-user-data")
	rt := env.FromEnviron([]string{"PATH=" + os.Getenv("PATH"), "XDG_DATA_HOME=" + dataHome, "PF_TEST_COMMAND_LOG=" + commands})
	dest, err := InstallRuntime(context.Background(), rt)
	if err != nil {
		t.Fatalf("runtime installation failed: %v", err)
	}
	if dest != filepath.Join(dataHome, extensionDirectory) {
		t.Fatalf("wrong user-data installation: %q", dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "metadata.json")); err != nil {
		t.Fatalf("bundled extension not published: %v", err)
	}
	data, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"get org.gnome.shell allow-extension-installation", "get org.gnome.shell disable-user-extensions", "get org.gnome.shell disabled-extensions", "get org.gnome.shell enabled-extensions", "set org.gnome.shell enabled-extensions"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing isolated settings action %q from %q", expected, text)
		}
	}
	if strings.Count(text, "\n") != 5 {
		t.Fatalf("unexpected extra GNOME host commands: %q", text)
	}
}

// GNOME extension identifiers are supplied by gsettings as quoted GVariant
// strings. Escapes must round-trip without turning one identifier into two.
func TestGSettingsExtensionQuoteAndParseRoundTrip(t *testing.T) {
	values := []string{
		"ordinary@example.org",
		"name'with-quote@example.org",
		`backslash\component@example.org`,
		`combined\'characters@example.org`,
		`double"quote@example.org`,
	}
	encoded := "[" + strings.Join(mapStrings(values, quoteGVariantString), ", ") + "]"
	if got := parseEnabledExtensions(encoded); !slices.Equal(got, values) {
		t.Fatalf("quoted GVariant round-trip: encoded=%q got=%q want=%q", encoded, got, values)
	}
}

func TestGSettingsExtensionParserAcceptsEscapedQuotedValues(t *testing.T) {
	input := `["gnome\\extension@example.org", 'quoted\'identifier@example.org', 'plain@example.org']`
	want := []string{`gnome\extension@example.org`, "quoted'identifier@example.org", "plain@example.org"}
	if got := parseEnabledExtensions(input); !slices.Equal(got, want) {
		t.Fatalf("parsed escaped GVariant values: got=%q want=%q", got, want)
	}
}

// A malformed caller must never panic or create files when requesting
// privileged GNOME Shell extension provisioning.
func TestGNOMEInstallerRejectsNilContextWithoutFilesystemMutation(t *testing.T) {
	dir := t.TempDir()
	runnerCalled := false
	installer := Installer{DataHome: dir, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) { runnerCalled = true; return nil, nil }}
	_, err := installer.Install(absentInstallerContext, env.FromEnviron(nil))
	if err == nil || !strings.Contains(err.Error(), "nil context") || runnerCalled {
		t.Fatalf("nil-context provisioning accepted; err=%v runner=%t", err, runnerCalled)
	}
}

// An intentionally absent context exercises the public nil-context boundary.
var absentInstallerContext context.Context

func TestRunGSettingsIsolatedRuntimeEnvironmentAndExitStatus(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "gsettings")
	contents := "#!/bin/sh\nprintf 'state=%s arguments=%s' \"$PF_SYNTHETIC_GSETTINGS_STATE\" \"$*\"\nif [ \"$1\" = fail ]; then printf ' denied' >&2; exit 23; fi\n"
	if err := os.WriteFile(script, []byte(contents), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PF_SYNTHETIC_GSETTINGS_STATE", "host-secret-must-not-leak")
	rt := envRuntimeForGSettings(bin, "runtime-scope")
	out, err := runGSettings(context.Background(), rt, "get", "org.gnome.shell", "enabled-extensions")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "state=runtime-scope arguments=get org.gnome.shell enabled-extensions" {
		t.Fatalf("wrong subprocess environment or arguments: %q", got)
	}
	out, err = runGSettings(context.Background(), rt, "fail", "org.gnome.shell")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 || !strings.Contains(string(out), "denied") {
		t.Fatalf("failed command: out=%q err=%v", out, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runGSettings(canceled, rt, "get", "org.gnome.shell")
	if err == nil {
		t.Fatal("canceled context launched gsettings without an error")
	}
}

func TestAtomicReplaceDirectoryRejectsWrongParentAndMissingSource(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one")
	two := filepath.Join(root, "two")
	if err := os.Mkdir(one, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(two, 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicReplaceDirectory(filepath.Join(one, "staging"), filepath.Join(two, "installed")); err == nil || !strings.Contains(err.Error(), "shared parent") {
		t.Fatalf("cross-directory replacement accepted: %v", err)
	}
	if err := atomicReplaceDirectory(filepath.Join(one, "missing"), filepath.Join(one, "installed")); err == nil {
		t.Fatal("missing staged extension accepted")
	}
}

func TestEmbeddedGNOMEExtensionNeverWritesThroughInvalidDestination(t *testing.T) {
	file := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(file, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeEmbeddedExtension(file); err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("non-directory destination accepted: %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("failed write altered original: %q %v", data, err)
	}
}

func envRuntimeForGSettings(bin, marker string) env.Runtime {
	return env.FromEnviron([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "PF_SYNTHETIC_GSETTINGS_STATE=" + marker})
}

// An existing regular file in the GNOME extension directory hierarchy is
// not an installation directory. Refuse provisioning without replacing that
// file, creating staging directories, or changing GNOME settings.
func TestGNOMEInstallerRejectsRegularFileDirectoryAncestorsWithoutMutation(t *testing.T) {
	for _, where := range []string{"gnome-shell", "extensions"} {
		t.Run(where, func(t *testing.T) {
			home := t.TempDir()
			ancestor := filepath.Join(home, "gnome-shell")
			if where == "extensions" {
				if err := os.Mkdir(ancestor, 0700); err != nil {
					t.Fatal(err)
				}
				ancestor = filepath.Join(ancestor, "extensions")
			}
			const marker = "existing user-owned content"
			if err := os.WriteFile(ancestor, []byte(marker), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			i := Installer{DataHome: home, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) { calls++; return nil, nil }}
			destination, err := i.Install(context.Background(), env.FromEnviron(nil))
			if destination != "" || !errors.Is(err, os.ErrExist) && (err == nil || !strings.Contains(err.Error(), "not a directory")) {
				t.Fatalf("non-directory ancestor accepted: destination=%q err=%v", destination, err)
			}
			if calls != 0 {
				t.Fatalf("denied install changed GNOME settings %d times", calls)
			}
			got, err := os.ReadFile(ancestor)
			if err != nil || string(got) != marker {
				t.Fatalf("original file changed: %q %v", got, err)
			}
			if entries, err := os.ReadDir(filepath.Dir(ancestor)); err != nil || len(entries) != 1 {
				t.Fatalf("failed installation created staging artifacts: %v %v", entries, err)
			}
		})
	}
}

func TestGNOMEInstallerRuntimeSourceAndFailClosedSelectionMatrix(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name      string
		env       []string
		wantDir   string
		wantError string
	}{
		{"XDG data home", []string{"XDG_DATA_HOME=" + filepath.Join(home, "xdg"), "HOME=" + home}, filepath.Join(home, "xdg"), ""},
		{"home fallback", []string{"HOME=" + home}, filepath.Join(home, ".local", "share"), ""},
		{"no home", nil, "", "XDG_DATA_HOME or HOME"},
		{"flatpak with XDG data", []string{"FLATPAK_ID=org.example.App", "XDG_DATA_HOME=" + home}, "", "Flatpak"},
		{"flatpak with home", []string{"FLATPAK_ID=org.example.App", "HOME=" + home}, "", "Flatpak"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installer, err := NewInstallerForRuntime(env.FromEnviron(tc.env))
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || installer.DataHome != "" {
					t.Fatalf("invalid installer target: %+v err=%v", installer, err)
				}
				return
			}
			if err != nil || installer.DataHome != tc.wantDir {
				t.Fatalf("installer runtime data home=%q want %q err=%v", installer.DataHome, tc.wantDir, err)
			}
		})
	}
}
func TestGNOMEInstallerRejectsMissingOrRelativeLocationAndCanceledMutation(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		canceled   bool
		want       string
	}{
		{"empty", "", false, "empty data directory"},
		{"relative", "relative-path", false, "absolute"},
		{"canceled", t.TempDir(), true, "context canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			called := false
			_, err := (Installer{DataHome: tc.path, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) { called = true; return nil, nil }}).Install(ctx, env.FromEnviron(nil))
			if err == nil || !strings.Contains(err.Error(), tc.want) || called {
				t.Fatalf("invalid install accepted: %v runner=%v", err, called)
			}
			if tc.name == "relative" {
				if _, err := os.Stat(filepath.Join(tc.path, "gnome-shell")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("relative installation wrote unexpected path: %v", err)
				}
			}
		})
	}
}
func TestGNOMEInstallerPublicHelpersRejectUnmanagedHostWithoutTouchingFiles(t *testing.T) {
	invalid := env.FromEnviron([]string{"FLATPAK_ID=org.example.App", "HOME=/nonexistent/private-home", "DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted"})
	if _, err := InstallRuntime(context.Background(), invalid); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Flatpak provisioning unexpectedly permitted: %v", err)
	}
	noData := env.FromEnviron(nil)
	if _, err := InstallRuntime(context.Background(), noData); err == nil || !strings.Contains(err.Error(), "HOME") {
		t.Fatalf("missing XDG data target not rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ConnectRuntime(ctx, invalid); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bridge connection did not propagate cancellation: %v", err)
	}
	if _, err := ConnectForCapability(ctx, invalid, CapabilityWindows); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capability probe did not propagate cancellation: %v", err)
	}
}

func TestGNOMEInstallerSettingsReadAndWriteFailuresPreservePublishedExtension(t *testing.T) {
	failure := errors.New("synthetic gsettings refused write or read")
	for _, tc := range []struct {
		name      string
		failAt    int
		disabled  string
		enabled   string
		wantStage string
	}{
		{"read installation policy", 1, "[]", "[]", "read extension-installation policy"},
		{"read user extension policy", 2, "[]", "[]", "read user-extension policy"},
		{"read disabled list", 3, "[]", "[]", "read disabled extensions"},
		{"clear disabled extension", 4, "['" + extensionUUID + "']", "[]", "clearing disabled state"},
		{"read enabled list", 4, "[]", "[]", "read enabled extensions"},
		{"write enabled list", 5, "[]", "[]", "enable extension for next session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataHome := t.TempDir()
			calls := 0
			runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
				calls++
				if calls == tc.failAt {
					return nil, failure
				}
				if args[0] == "set" {
					return nil, nil
				}
				switch args[2] {
				case "allow-extension-installation":
					return []byte("true"), nil
				case "disable-user-extensions":
					return []byte("false"), nil
				case "disabled-extensions":
					return []byte(tc.disabled), nil
				case "enabled-extensions":
					return []byte(tc.enabled), nil
				default:
					t.Fatalf("unexpected settings key %q", args[2])
					return nil, nil
				}
			}
			installer := Installer{DataHome: dataHome, Run: runner}
			path, err := installer.Install(context.Background(), env.FromEnviron([]string{"HOME=" + dataHome}))
			if !errors.Is(err, failure) || !strings.Contains(err.Error(), tc.wantStage) {
				t.Fatalf("settings failure not surfaced: %v", err)
			}
			wantPath := filepath.Join(dataHome, extensionDirectory)
			if path != wantPath {
				t.Fatalf("already installed extension path not returned: %q want %q", path, wantPath)
			}
			if _, err := os.Stat(filepath.Join(path, "metadata.json")); err != nil {
				t.Fatalf("gsettings failure corrupted staged extension: %v", err)
			}
			if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".perfuncted-extension-*")); len(matches) != 0 {
				t.Fatalf("gsettings failure leaked staging directories: %v", matches)
			}
		})
	}
}

func TestGnomeExtensionInstallerRejectsSymlinkedDestinationAncestors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		linkAt string
	}{
		{"data home", "data-home"},
		{"gnome shell parent", "gnome-shell"},
		{"extension parent", "extensions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "data")
			outside := filepath.Join(root, "unrelated-directory")
			if err := os.MkdirAll(outside, 0700); err != nil {
				t.Fatal(err)
			}
			if tc.linkAt == "data-home" {
				if err := os.Symlink(outside, destination); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(destination, 0700); err != nil {
					t.Fatal(err)
				}
				if tc.linkAt == "gnome-shell" {
					if err := os.Symlink(outside, filepath.Join(destination, "gnome-shell")); err != nil {
						t.Fatal(err)
					}
				} else {
					shell := filepath.Join(destination, "gnome-shell")
					if err := os.Mkdir(shell, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, filepath.Join(shell, "extensions")); err != nil {
						t.Fatal(err)
					}
				}
			}
			var invoked int
			installer := Installer{DataHome: destination, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) { invoked++; return []byte("true"), nil }}
			path, err := installer.Install(context.Background(), env.FromEnviron(nil))
			if err == nil || path != "" || invoked != 0 {
				t.Fatalf("extension install followed symlink ancestor: path=%q err=%v gsettings-calls=%d", path, err, invoked)
			}
			if _, err := os.Stat(filepath.Join(outside, extensionUUID)); !os.IsNotExist(err) {
				t.Fatalf("unrelated directory was modified through symlink: %v", err)
			}
			if tc.linkAt == "data-home" {
				if _, err := os.Stat(filepath.Join(outside, "gnome-shell")); !os.IsNotExist(err) {
					t.Fatalf("unrelated data home was modified: %v", err)
				}
			}
		})
	}
}

func TestInstallerConflictingExtensionTargetCleansStagingWithoutChangingSettings(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "gnome-shell", "extensions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, extensionUUID)
	const original = "user-owned non-extension file"
	if err := os.WriteFile(target, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	installer := Installer{DataHome: home, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) {
		calls++
		return nil, nil
	}}
	path, err := installer.Install(context.Background(), env.FromEnviron(nil))
	if path != "" || err == nil || !strings.Contains(err.Error(), "existing extension target is not a directory") {
		t.Fatalf("conflicting destination = (%q, %v)", path, err)
	}
	if calls != 0 {
		t.Fatalf("failed installation changed settings %d times", calls)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != original {
		t.Fatalf("conflicting target changed: %q, %v", contents, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != extensionUUID {
		t.Fatalf("failed installation left staging artifacts: %v, %v", entries, err)
	}
}

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

// A user may revoke write permission on an existing extension directory.
// Provisioning must not overwrite an existing extension or run gsettings when
// it cannot stage an atomic replacement in that directory.
func TestGNOMEInstallerRejectsUnwritableExtensionRootBeforeSettingsMutation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	home := t.TempDir()
	root := filepath.Join(home, "gnome-shell", "extensions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, extensionUUID)
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "private-user-data")
	if err := os.WriteFile(marker, []byte("keep this content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(root, 0o700) }()
	calls := 0
	installer := Installer{DataHome: home, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) {
		calls++
		return nil, errors.New("gsettings must not run after failed staging")
	}}
	path, err := installer.Install(context.Background(), env.FromEnviron(nil))
	if path != "" || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unwritable extension staging accepted: path=%q err=%v", path, err)
	}
	if calls != 0 {
		t.Fatalf("failed extension staging invoked gsettings %d times", calls)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep this content" {
		t.Fatalf("failed staging changed the existing extension: data=%q err=%v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != extensionUUID {
		t.Fatalf("failed staging left unexpected artifacts: entries=%v err=%v", entries, err)
	}
}

// Atomic replacement can succeed while removing the displaced extension
// fails. Report that cleanup failure without deleting the old inaccessible
// directory or mistaking it for the newly installed version.
func TestGNOMEAtomicReplacementReportsDisplacedExtensionCleanupDenial(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	dest := filepath.Join(parent, extensionUUID)
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "installed"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "previous"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dest, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(stage, 0o700) }()

	err := atomicReplaceDirectory(stage, dest)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("failed old-extension cleanup ignored: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(dest, "installed")); err != nil || string(contents) != "new" {
		t.Fatalf("installed content not published despite directory swap: %q %v", contents, err)
	}
	if contents, err := os.ReadFile(filepath.Join(stage, "previous")); err != nil || string(contents) != "old" {
		t.Fatalf("displaced extension lost or altered after cleanup denial: %q %v", contents, err)
	}
}
