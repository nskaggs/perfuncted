//go:build linux

package window

import (
	"context"
	"errors"
	"flag"
	"github.com/godbus/dbus/v5"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func BenchmarkKWinMoveAction(b *testing.B) {
	for b.Loop() {
		_ = kwinMoveAction(100, 200)
	}
}

// A service registered under KWin's bus name may deny introspection or
// disappear before a client creates its manager. Neither case grants
// authority to execute desktop automation scripts.
func TestKWinManagerConstructorRejectsDeniedOrRevokedPrivateService(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	if err := provider.conn.Export(deniedKWinIntrospection{}, kwinScriptPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewKWinScriptManagerForBusContext(context.Background(), provider.busAddress)
	if manager != nil {
		_ = manager.Close()
		t.Fatal("denied private introspection created KWin manager")
	}
	if err == nil || !strings.Contains(err.Error(), "synthetic private scripting introspection denied") {
		t.Fatalf("introspection failure lost or authorized: %v", err)
	}
	reply, err := provider.conn.ReleaseName(kwinScriptSvc)
	if err != nil || reply != dbus.ReleaseNameReplyReleased {
		t.Fatalf("release KWin service: %v %v", reply, err)
	}
	manager, err = NewKWinScriptManagerForBusContext(context.Background(), provider.busAddress)
	if manager != nil {
		_ = manager.Close()
		t.Fatal("revoked KWin service created manager")
	}
	if err == nil || !strings.Contains(err.Error(), "KWin Scripting not on session bus") {
		t.Fatalf("revoked scripting service not rejected: %v", err)
	}
}

func TestKWinScriptStagingFailureRemovesPrivateFile(t *testing.T) {
	const childEnv = "PF_TEST_KWIN_FILE_SIZE_LIMIT"
	if os.Getenv(childEnv) == "1" {
		signal.Ignore(syscall.SIGXFSZ)
		var original syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		limited.Cur = 0
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
				t.Errorf("restore file size limit: %v", err)
			}
		}()
		path, err := writeKWinScript(func(string) string {
			return "callDBus('org.kde.private', '/', 'org.kde.private', 'ReportWindows', 'private');"
		}, "org.kde.private")
		if path != "" || !errors.Is(err, syscall.EFBIG) || !strings.Contains(err.Error(), "write temp file") {
			t.Fatalf("quota failure returned path %q, err %v", path, err)
		}
		entries, err := os.ReadDir(os.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("failed script staging retained private artifacts: %v", entries)
		}
		return
	}

	dir := t.TempDir()
	args := []string{"-test.run=^TestKWinScriptStagingFailureRemovesPrivateFile$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1", "TMPDIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated quota test failed: %v\n%s", err, output)
	}
}

// A KWin script can invoke the one-shot callback more than once. The callback
// handler must not block the D-Bus dispatch thread or replace the first result
// with a later, potentially unrelated window observation.
func TestKWinScriptReceiverIgnoresDuplicateCallbacksWithoutBlocking(t *testing.T) {
	receiver := &pfReceiver{ch: make(chan string, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := receiver.ReportWindows(`{"id":"first"}`); err != nil {
			t.Errorf("first report returned D-Bus error: %v", err)
		}
		if err := receiver.ReportWindows(`{"id":"second"}`); err != nil {
			t.Errorf("duplicate report returned D-Bus error: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("duplicate callback blocked the D-Bus dispatch path")
	}
	select {
	case got := <-receiver.ch:
		if got != `{"id":"first"}` {
			t.Fatalf("duplicate overwrote first response: %q", got)
		}
	default:
		t.Fatal("first callback was dropped")
	}
	select {
	case got := <-receiver.ch:
		t.Fatalf("duplicate enqueued extra callback: %q", got)
	default:
	}
}

func TestKWinRunScriptReportsTemporaryScriptCleanupFailure(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	manager, provider := newSyntheticKWinManager(t)
	provider.mu.Lock()
	provider.replaceLoadedScriptPath = true
	provider.mu.Unlock()

	result, err := manager.ActiveTitle(context.Background())
	if result != "Editor" {
		t.Fatalf("script result = %q, want callback result despite cleanup failure", result)
	}
	if !errors.Is(err, syscall.ENOTEMPTY) || !strings.Contains(err.Error(), "remove temp script") {
		t.Fatalf("script error = %v, want surfaced temp-script cleanup failure", err)
	}
}

func TestKWinRunScriptJoinsCleanupErrorsAfterBusDisconnect(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	provider.mu.Lock()
	provider.closeClientConnOnStart = manager.conn
	provider.mu.Unlock()

	result, err := manager.ActiveTitle(context.Background())
	if result != "" {
		t.Fatalf("script result = %q, want no result after D-Bus disconnect", result)
	}
	if err == nil || !strings.Contains(err.Error(), "unloadScript") || !strings.Contains(err.Error(), "release D-Bus name") {
		t.Fatalf("script error = %v, want start and cleanup failures joined", err)
	}
}

func TestRequestKWinNameRejectsNameOwnedBySameConnection(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	const serviceName = "org.kde.pflistCoverageDuplicate"
	reply, err := provider.conn.RequestName(serviceName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim synthetic callback name: reply=%v err=%v", reply, err)
	}
	t.Cleanup(func() { _, _ = provider.conn.ReleaseName(serviceName) })

	err = requestKWinName(context.Background(), provider.conn, serviceName)
	if err == nil || !strings.Contains(err.Error(), "already taken") || !strings.Contains(err.Error(), serviceName) {
		t.Fatalf("duplicate RequestName error = %v, want already-taken error for %s", err, serviceName)
	}
}

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
