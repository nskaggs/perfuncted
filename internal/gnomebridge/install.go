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
	"strings"

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

// ConnectRuntime returns the running bridge, installing the bundled extension
// when GNOME has not loaded it yet. Installation deliberately returns a typed
// restart condition: a live Shell cannot be assumed to load a newly installed
// extension in the current session.
func ConnectRuntime(ctx context.Context, rt env.Runtime) (*Client, error) {
	address := rt.Get("DBUS_SESSION_BUS_ADDRESS")
	client, err := NewClientForBus(ctx, address)
	if err == nil {
		// Protocol compatibility is sufficient to keep using the live bridge.
		// Refresh an older bundled copy opportunistically; GNOME Shell will load
		// those files at the next normal session boundary.
		if client.ExtensionVersion() != ExtensionVersion {
			_, _ = InstallRuntime(ctx, rt)
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
