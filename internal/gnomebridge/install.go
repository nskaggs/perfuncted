//go:build linux
// +build linux

package gnomebridge

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
)

const extensionDirectory = "gnome-shell/extensions/" + extensionUUID

const extensionUUID = "perfuncted@nskaggs.github.io"

// The extension is deliberately embedded in the executable so release
// binaries, go install, and Flatpak all carry the same privileged adapter.
//
//go:embed assets/perfuncted@nskaggs.github.io
var extensionAssets embed.FS

// Installer writes the bundled extension to one GNOME user-data directory.
// Run is injectable for provisioning tests; a nil Run invokes gsettings with
// the supplied runtime environment.
type Installer struct {
	DataHome string
	Run      func(context.Context, env.Runtime, ...string) ([]byte, error)
}

// NewInstallerForRuntime selects XDG_DATA_HOME, then HOME/.local/share, from
// the target runtime snapshot. It never uses a second host-local environment.
func NewInstallerForRuntime(rt env.Runtime) (Installer, error) {
	if rt.Get("FLATPAK_ID") != "" {
		return Installer{}, fmt.Errorf("%w: host GNOME extension provisioning from Flatpak is unavailable; install the native perfuncted package once", ErrUnavailable)
	}
	dataHome := rt.Get("XDG_DATA_HOME")
	if dataHome == "" {
		if home := rt.Get("HOME"); home != "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	if dataHome == "" {
		return Installer{}, fmt.Errorf("gnome bridge: XDG_DATA_HOME or HOME is required to install the extension")
	}
	return Installer{DataHome: dataHome}, nil
}

// InstallRuntime installs the embedded extension and enables it for the next
// GNOME Shell session.
func InstallRuntime(ctx context.Context, rt env.Runtime) (string, error) {
	installer, err := NewInstallerForRuntime(rt)
	if err != nil {
		return "", err
	}
	return installer.Install(ctx, rt)
}

// ConnectForCapability dials the bridge and verifies capability is advertised.
func ConnectForCapability(ctx context.Context, rt env.Runtime, capability string) (*Client, error) {
	bridge, err := ConnectRuntime(ctx, rt)
	if err != nil {
		return nil, err
	}
	if !bridge.HasCapability(capability) {
		_ = bridge.Close()
		return nil, fmt.Errorf("gnome bridge does not advertise %s capability", capability)
	}
	return bridge, nil
}

// ConnectRuntime returns the running bridge, installing the bundled extension
// when GNOME has not loaded it yet. Installation deliberately returns a typed
// restart condition: a live Shell cannot be assumed to load a newly installed
// extension in the current session.
func ConnectRuntime(ctx context.Context, rt env.Runtime) (*Client, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	address := rt.Get("DBUS_SESSION_BUS_ADDRESS")
	client, err := NewClientForBus(ctx, address)
	if err == nil {
		if extensionVersionNeedsUpdate(client.ExtensionVersion(), ExtensionVersion) {
			runningVersion := client.ExtensionVersion()
			path, installErr := InstallRuntime(ctx, rt)
			_ = client.Close()
			if installErr != nil {
				return nil, fmt.Errorf("%w: running GNOME bridge extension %q is obsolete; refresh it before use: %w", ErrUnavailable, runningVersion, installErr)
			}
			return nil, &SessionRestartRequiredError{Path: path}
		}
		return client, nil
	}
	if !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrProtocolMismatch) {
		return nil, err
	}
	if address == "" {
		return nil, err
	}
	path, installErr := InstallRuntime(ctx, rt)
	if installErr != nil {
		return nil, fmt.Errorf("%w: %w; install bundled extension: %w", ErrUnavailable, err, installErr)
	}
	return nil, &SessionRestartRequiredError{Path: path}
}

// extensionVersionNeedsUpdate reports whether the running bridge is an older
// numeric version than the one bundled with this binary. Unknown versions are
// left untouched so an older or development binary cannot replace a newer
// bridge merely because its version string differs.
func extensionVersionNeedsUpdate(running, bundled string) bool {
	runningVersion, err := strconv.ParseUint(strings.TrimSpace(running), 10, 64)
	if err != nil {
		return false
	}
	bundledVersion, err := strconv.ParseUint(strings.TrimSpace(bundled), 10, 64)
	if err != nil {
		return false
	}
	return runningVersion < bundledVersion
}

// Install atomically replaces the exact bundled extension directory and then
// adds its UUID to GNOME's enabled-extensions setting without disturbing
// other extensions.
func (i Installer) Install(ctx context.Context, rt env.Runtime) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if i.DataHome == "" {
		return "", fmt.Errorf("gnome bridge: empty data directory")
	}
	root := filepath.Join(i.DataHome, "gnome-shell", "extensions")
	dest := filepath.Join(root, extensionUUID)
	if !filepath.IsAbs(dest) {
		return "", fmt.Errorf("gnome bridge: extension path must be absolute")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("gnome bridge: create extension directory: %w", err)
	}
	tmp, err := os.MkdirTemp(root, ".perfuncted-extension-")
	if err != nil {
		return "", fmt.Errorf("gnome bridge: create temporary extension directory: %w", err)
	}
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := writeEmbeddedExtension(tmp); err != nil {
		return "", err
	}
	if err := atomicReplaceDirectory(tmp, dest); err != nil {
		return "", err
	}
	keepTemp = false
	if err := i.ensureEnabled(ctx, rt); err != nil {
		return dest, err
	}
	return dest, nil
}

func writeEmbeddedExtension(dest string) error {
	entries, err := fs.ReadDir(extensionAssets, "assets/"+extensionUUID)
	if err != nil {
		return fmt.Errorf("gnome bridge: read embedded extension: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("gnome bridge: embedded extension contains unexpected directory %q", entry.Name())
		}
		data, err := fs.ReadFile(extensionAssets, "assets/"+extensionUUID+"/"+entry.Name())
		if err != nil {
			return fmt.Errorf("gnome bridge: read embedded %s: %w", entry.Name(), err)
		}
		path := filepath.Join(dest, entry.Name())
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("gnome bridge: write %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func atomicReplaceDirectory(tmp, dest string) error {
	backup := dest + ".old"
	_ = os.RemoveAll(backup)
	if err := os.Rename(dest, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("gnome bridge: stage existing extension: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, dest)
		}
		return fmt.Errorf("gnome bridge: install extension: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("gnome bridge: remove old extension: %w", err)
	}
	return nil
}

func (i Installer) ensureEnabled(ctx context.Context, rt env.Runtime) error {
	run := i.Run
	if run == nil {
		run = runGSettings
	}
	allowInstallation, err := run(ctx, rt, "get", "org.gnome.shell", "allow-extension-installation")
	if err != nil {
		return fmt.Errorf("gnome bridge: read extension-installation policy: %w", err)
	}
	if !parseGSettingsBool(string(allowInstallation)) {
		return fmt.Errorf("%w: GNOME extension installation is disabled by org.gnome.shell allow-extension-installation; set it to true or ask the system administrator", ErrUnavailable)
	}
	disabledUserExtensions, err := run(ctx, rt, "get", "org.gnome.shell", "disable-user-extensions")
	if err != nil {
		return fmt.Errorf("gnome bridge: read user-extension policy: %w", err)
	}
	if parseGSettingsBool(string(disabledUserExtensions)) {
		return fmt.Errorf("%w: GNOME user extensions are disabled by org.gnome.shell disable-user-extensions; set it to false and retry", ErrUnavailable)
	}
	disabledOut, err := run(ctx, rt, "get", "org.gnome.shell", "disabled-extensions")
	if err != nil {
		return fmt.Errorf("gnome bridge: read disabled extensions: %w", err)
	}
	disabled := parseEnabledExtensions(string(disabledOut))
	if filtered, changed := removeString(disabled, extensionUUID); changed {
		value := "[" + strings.Join(mapStrings(filtered, quoteGVariantString), ", ") + "]"
		if _, setErr := run(ctx, rt, "set", "org.gnome.shell", "disabled-extensions", value); setErr != nil {
			return fmt.Errorf("gnome bridge: enable extension by clearing disabled state: %w", setErr)
		}
	}
	out, err := run(ctx, rt, "get", "org.gnome.shell", "enabled-extensions")
	if err != nil {
		return fmt.Errorf("gnome bridge: read enabled extensions: %w", err)
	}
	current := parseEnabledExtensions(string(out))
	if slices.Contains(current, extensionUUID) {
		return nil
	}
	current = append(current, extensionUUID)
	value := "[" + strings.Join(mapStrings(current, quoteGVariantString), ", ") + "]"
	if _, err := run(ctx, rt, "set", "org.gnome.shell", "enabled-extensions", value); err != nil {
		return fmt.Errorf("gnome bridge: enable extension for next session: %w", err)
	}
	return nil
}

func parseGSettingsBool(value string) bool {
	return strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "@b ")), "true")
}

func removeString(values []string, target string) ([]string, bool) {
	filtered := make([]string, 0, len(values))
	changed := false
	for _, value := range values {
		if value == target {
			changed = true
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered, changed
}

func runGSettings(ctx context.Context, rt env.Runtime, args ...string) ([]byte, error) {
	cmd := executil.CommandContext(ctx, "gsettings", args...)
	cmd.Env = rt.EnvList()
	return cmd.CombinedOutput()
}

func parseEnabledExtensions(value string) []string {
	var extensions []string
	for i := 0; i < len(value); {
		if value[i] != '\'' && value[i] != '"' {
			i++
			continue
		}
		quote := value[i]
		i++
		var builder strings.Builder
		for i < len(value) {
			if value[i] == '\\' && i+1 < len(value) {
				builder.WriteByte(value[i+1])
				i += 2
				continue
			}
			if value[i] == quote {
				i++
				break
			}
			builder.WriteByte(value[i])
			i++
		}
		if builder.Len() != 0 {
			extensions = append(extensions, builder.String())
		}
	}
	return extensions
}

func quoteGVariantString(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "\\'") + "'"
}

func mapStrings(values []string, fn func(string) string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = fn(value)
	}
	return out
}
