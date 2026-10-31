//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

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
