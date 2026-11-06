//go:build linux

package window

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// KWin script execution acquires a D-Bus name and script registration, both
// resources with longer lifetimes than a single attempted window query. Each
// failed startup stage must release what it acquired and leave the connection
// capable of running a subsequent authorized operation.
func TestKWinFailedScriptStartupUnloadsAndAllowsRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		configure                          func(*syntheticKWinScriptService)
		want                               string
		wantLoads, wantStarts, wantUnloads int
	}{
		{"load rejected", func(s *syntheticKWinScriptService) { s.failLoad = true }, "loadScript", 0, 0, 0},
		{"start rejected", func(s *syntheticKWinScriptService) { s.failStart = true }, "start", 1, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, provider := newSyntheticKWinManager(t)
			provider.mu.Lock()
			tc.configure(provider)
			provider.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			title, err := manager.ActiveTitle(ctx)
			if title != "" || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("failed script returned window title %q error %v", title, err)
			}
			provider.mu.Lock()
			loads, starts, unloads, path := provider.loadCount, provider.startCount, provider.unloadCount, provider.loadedPath
			provider.failLoad = false
			provider.failStart = false
			provider.mu.Unlock()
			if loads != tc.wantLoads || starts != tc.wantStarts || unloads != tc.wantUnloads {
				t.Fatalf("failed script resource lifecycle loads=%d starts=%d unloads=%d, want %d,%d,%d", loads, starts, unloads, tc.wantLoads, tc.wantStarts, tc.wantUnloads)
			}
			if path != "" {
				if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("failed script left temporary JS file at %q: %v", path, statErr)
				}
			}
			recovered, err := manager.ActiveTitle(ctx)
			if err != nil || recovered != "Editor" {
				t.Fatalf("failed start poisoned subsequent authorized query: %q %v", recovered, err)
			}
			provider.mu.Lock()
			finalLoads, finalUnloads := provider.loadCount, provider.unloadCount
			provider.mu.Unlock()
			if finalLoads != tc.wantLoads+1 || finalUnloads != tc.wantUnloads+1 {
				t.Fatalf("successful retry leaked scripts: loads=%d unloads=%d", finalLoads, finalUnloads)
			}
		})
	}
}

func TestKWinScriptTemporaryDirectoryFailureReleasesDBusName(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	original := os.Getenv("TMPDIR")
	t.Cleanup(func() {
		if original == "" {
			_ = os.Unsetenv("TMPDIR")
		} else {
			_ = os.Setenv("TMPDIR", original)
		}
	})
	blocker := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(blocker, []byte("must remain untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(blocker, "bad-temporary-scripts"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if title, err := manager.ActiveTitle(ctx); title != "" || err == nil || !strings.Contains(err.Error(), "temp file") {
		t.Fatalf("inaccessible script temporary directory granted window metadata: %q %v", title, err)
	}
	if data, err := os.ReadFile(blocker); err != nil || string(data) != "must remain untouched" {
		t.Fatalf("failed script modified foreign path: %q %v", data, err)
	}
	provider.mu.Lock()
	loads, starts := provider.loadCount, provider.startCount
	provider.mu.Unlock()
	if loads != 0 || starts != 0 {
		t.Fatalf("script executed without safely staged file: loads=%d starts=%d", loads, starts)
	}
	if err := os.Setenv("TMPDIR", original); err != nil {
		t.Fatal(err)
	}
	recovered, err := manager.ActiveTitle(ctx)
	if err != nil || recovered != "Editor" {
		t.Fatalf("failed staging leaked D-Bus registration: %q %v", recovered, err)
	}
}

func TestKWinScriptingConstructorRejectsUntrustedIntrospection(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	if err := provider.conn.Export(syntheticAbsentKWinIntrospection{}, kwinScriptPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewKWinScriptManagerForBusContext(context.Background(), provider.busAddress)
	if manager != nil || err == nil || !strings.Contains(err.Error(), "interface absent") {
		if manager != nil {
			_ = manager.Close()
		}
		t.Fatalf("untrusted service introspection was accepted: %v, %v", manager, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	manager, err = NewKWinScriptManagerForBusContext(canceled, provider.busAddress)
	if manager != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constructor reached session bus: %v %v", manager, err)
	}
}
