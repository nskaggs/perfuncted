//go:build linux

package window

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"os"
	"strings"
	"testing"
	"time"
)

// A ready callback cannot authorize a fresh window observation if the
// operation expires between initial validation and callback publication.
type kwinCancelAfterFirstCheck struct {
	context.Context //nolint:containedctx // Controls an exact, synthetic cancellation boundary.
	checks          int
}

func (c *kwinCancelAfterFirstCheck) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}

func TestKWinReadyCallbackRejectedWhenCallerCanceledAfterAdmission(t *testing.T) {
	ctx := &kwinCancelAfterFirstCheck{Context: context.Background()}
	recv := &pfReceiver{ch: make(chan string, 1)}
	recv.ch <- `[{"id":"42","title":"stale observation"}]`
	value, err := waitForKWinResult(ctx, recv, 42)
	if value != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation published callback=%q err=%v", value, err)
	}
	if ctx.checks != 2 {
		t.Fatalf("cancellation checked %d times, want admission and publication", ctx.checks)
	}
}

// KWin window handles must identify exactly one compositor window. A corrupt
// or stale scripting callback must not publish ambiguous title/process data.
func TestKWinWindowListRefusesDuplicateAndNonIdentifyingHandles(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"duplicate identical native id", `[{"id":"7","title":"Private A"},{"id":"7","title":"Private B"}]`},
		{"duplicate opaque native id", `[{"id":"{private-uuid}","title":"Private A"},{"id":"{private-uuid}","title":"Private B"}]`},
		{"blank native id", `[{"id":"   ","title":"Private"}]`},
		{"tab native id", `[{"id":"\t","title":"Private"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			windows, err := parseKWinWindowList(tc.body)
			if err == nil || len(windows) != 0 {
				t.Fatalf("unidentifiable window metadata accepted: %+v err=%v", windows, err)
			}
		})
	}
}

func TestKWinWindowListPreservesDistinctOpaqueAndNumericHandles(t *testing.T) {
	windows, err := parseKWinWindowList(`[{"id":"7","title":"Numeric"},{"id":"07","title":"Leading zero"},{"id":"{opaque}","title":"Opaque"}]`)
	if err != nil || len(windows) != 3 {
		t.Fatalf("distinct IDs rejected: %+v %v", windows, err)
	}
	for i, want := range []string{"7", "07", "{opaque}"} {
		if windows[i].StableID() != want || !strings.Contains(windows[i].Title, []string{"Numeric", "Leading zero", "Opaque"}[i]) {
			t.Fatalf("lost stable identity: %+v want %s", windows[i], want)
		}
	}
}

// The provider may return multiple windows in one KWin callback. Cancelling
// while consuming the first must not disclose subsequent window metadata.
func TestKWinPrivateBusIteratorStopsPublishingWindowsAfterConsumerCancellation(t *testing.T) {
	m, service := newSyntheticKWinManager(t)
	service.mu.Lock()
	service.listOverride = `[{"id":"7","title":"First private window"},{"id":"8","title":"Second private window"}]`
	service.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	observedCancel := false
	for info, err := range m.IterateWindows(ctx) {
		if count == 0 {
			if err != nil || info.Title != "First private window" {
				t.Fatalf("initial window missing: %+v %v", info, err)
			}
			count++
			cancel()
			continue
		}
		if errors.Is(err, context.Canceled) && info == (Info{}) {
			observedCancel = true
			break
		}
		t.Fatalf("KWin leaked window after consumer cancellation: %+v %v", info, err)
	}
	if count != 1 || !observedCancel {
		t.Fatalf("cancellation was not surfaced during enumeration: count=%d canceled=%v", count, observedCancel)
	}
}

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

// A callback can arrive concurrently with caller cancellation. Once canceled,
// the caller must not receive a "verified" window list from an expired query.
func TestKWinWaitForResultReadyCallbackMustNotOutrunCanceledCaller(t *testing.T) {
	for i := 0; i < 128; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		receiver := &pfReceiver{ch: make(chan string, 1)}
		receiver.ch <- `[{"id":"7","title":"stale"}]`
		cancel()
		response, err := waitForKWinResult(ctx, receiver, 7)
		if !errors.Is(err, context.Canceled) || response != "" {
			t.Fatalf("iteration %d accepted stale callback after cancellation: response=%q error=%v", i, response, err)
		}
	}
}

// Exercise the actual temporary-script callback and cleanup protocol for
// handle lookups rather than using an in-memory fake window manager.
func TestKWinPrivateBusInfoByIDIteratorOperationsAndCleanup(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info, err := manager.InfoByID(ctx, "7")
	if err != nil || info.NativeID != "7" || info.Title != "Editor" || info.PID != 33 || info.X != -100 {
		t.Fatalf("native handle resolution %+v %v", info, err)
	}
	if _, err := manager.InfoByID(ctx, "nonexistent-window"); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("nonexistent native handle accepted: %v", err)
	}
	count := 0
	for win, err := range manager.IterateWindows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if win.NativeID != "7" {
			t.Fatalf("wrong native window %q", win.NativeID)
		}
		break
	}
	if count != 1 {
		t.Fatalf("iterator produced %d windows", count)
	}
	if err := manager.FullscreenByID(ctx, "7"); err != nil {
		t.Fatalf("fullscreen synthetic compositor script failed: %v", err)
	}
	if err := manager.UnfullscreenByID(ctx, "7"); err != nil {
		t.Fatalf("unfullscreen synthetic compositor script failed: %v", err)
	}
	if err := manager.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	ops := manager.SupportedOperations()
	for _, required := range []string{"discover", "info", "active-title", "activate", "move", "resize", "close", "minimize", "maximize", "restore", "fullscreen", "unfullscreen", "list-desktops", "switch-desktop", "set-window-desktop", "keep-above", "keep-below", "cursor-position"} {
		found := false
		for _, op := range ops {
			if op == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("KWin advertised ops missing %q: %v", required, ops)
		}
	}
	provider.mu.Lock()
	loads, starts, unloads, path := provider.loadCount, provider.startCount, provider.unloadCount, provider.loadedPath
	provider.mu.Unlock()
	if loads != 5 || starts != 5 || unloads != 5 {
		t.Fatalf("KWin native lookup leaked scripts: loads=%d starts=%d unloads=%d", loads, starts, unloads)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("KWin script temp file leaked: %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := manager.InfoByID(canceled, "7"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled native handle query: %v", err)
	}
	if err := manager.Sync(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled KWin sync: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.InfoByID(ctx, "7"); err == nil {
		t.Fatal("closed KWin transport accepted new command")
	}
}
func TestKWinPrivateBusListAndActionFailuresNeverFabricateWindow(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	provider.mu.Lock()
	provider.noCallback = true
	provider.mu.Unlock()
	short, stop := context.WithTimeout(ctx, 60*time.Millisecond)
	defer stop()
	if _, err := manager.List(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("callback timeout reported verified empty list: %v", err)
	}
	provider.mu.Lock()
	provider.noCallback = false
	loads, unloads := provider.loadCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != unloads {
		t.Fatalf("failed list leaked KWin script: loads=%d unloads=%d", loads, unloads)
	}
	if err := manager.MoveByID(ctx, "7", -100, 30); err != nil {
		t.Fatal(err)
	}
	if err := kwinActionResultByID("7", kwinScriptErrorPrefix+"synthetic compositor rejected movement"); err == nil || !strings.Contains(err.Error(), "rejected movement") {
		t.Fatalf("explicit KWin script error not propagated %v", err)
	}
	if err := kwinActionResultByID("7", ""); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("KWin empty callback fabricated window: %v", err)
	}
}

// A KWin compositor that reports no matching virtual desktop must return the
// desktop-specific sentinel, not success or an unrelated missing-window error.
// Rejected operations must release their per-request scripts and must not
// poison the same manager's subsequent legitimate desktop selection.
func TestKWinSwitchDesktopDistinguishesMissingDesktopAndRecovers(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	provider.mu.Lock()
	provider.actionOverride = "__pf_desktop_missing__"
	provider.mu.Unlock()
	err := manager.SwitchDesktop(ctx, "nonexistent-desktop")
	if !errors.Is(err, ErrDesktopNotFound) || errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("missing compositor desktop accepted or misclassified: %v", err)
	}
	provider.mu.Lock()
	provider.actionOverride = ""
	provider.mu.Unlock()
	if err := manager.SwitchDesktop(ctx, "desktop-1"); err != nil {
		t.Fatalf("missing-desktop response poisoned next selection: %v", err)
	}
	provider.mu.Lock()
	loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != 2 || starts != 2 || unloads != 2 {
		t.Fatalf("desktop selection leaked scripts: loads=%d starts=%d unloads=%d", loads, starts, unloads)
	}
}

// Exercise the public KWin desktop method through the actual private D-Bus
// scripting lifecycle. Parsed desktop state must come from its callback, not
// the session's desktop labels or a previously cached response.
func TestKWinListDesktopsPrivateBusCallbackAndValidation(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	got, err := manager.ListDesktops(ctx)
	if err != nil || len(got) != 2 || got[0].ID != "desktop-1" || !got[0].Current || got[1].ID != "desktop-2" || got[1].Current {
		t.Fatalf("KWin desktop callback: got=%+v err=%v", got, err)
	}

	for _, tc := range []struct{ name, data, reason string }{
		{"untrusted non-array", `{"id":"desktop-1"}`, "cannot unmarshal"},
		{"null current", `[{"id":"desktop-1","position":1,"current":null}]`, "null current"},
		{"ambiguous current", `[{"id":"desktop-1","position":1,"current":true},{"id":"desktop-2","position":2,"current":true}]`, "one current"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.listOverride = tc.data
			provider.mu.Unlock()
			desktops, err := manager.ListDesktops(ctx)
			if err == nil || desktops != nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("accepted invalid desktop response: got=%+v err=%v", desktops, err)
			}
		})
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.loadCount != 4 || provider.startCount != 4 || provider.unloadCount != 4 {
		t.Fatalf("KWin scripts leaked: loads=%d starts=%d unloads=%d", provider.loadCount, provider.startCount, provider.unloadCount)
	}
}

// A D-Bus script start failure must not be interpreted as a successful
// desktop mutation, an empty desktop inventory, or a valid cursor location.
// Every method must release the temporary registered script and permit a
// subsequent authorized request on the same manager.
func TestKWinDesktopOperationsRejectFailedScriptStartAndRecover(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	operations := []struct {
		name string
		run  func() error
	}{
		{"list-desktops", func() error { _, err := manager.ListDesktops(ctx); return err }},
		{"switch-desktop", func() error { return manager.SwitchDesktop(ctx, "desktop-1") }},
		{"assign-window-desktop", func() error { return manager.SetWindowDesktopByID(ctx, "7", "desktop-2") }},
		{"keep-above", func() error { return manager.SetKeepAboveByID(ctx, "7", true) }},
		{"keep-below", func() error { return manager.SetKeepBelowByID(ctx, "7", true) }},
		{"cursor-position", func() error {
			x, y, err := manager.CursorPosition(ctx)
			if x != 0 || y != 0 {
				t.Errorf("failed cursor lookup leaked %d,%d", x, y)
			}
			return err
		}},
		{"shade", func() error { return manager.SetShadeByID(ctx, "7", true) }},
		{"tile", func() error { return manager.TileWindowByID(ctx, "7", 10, 20) }},
		{"float", func() error { return manager.FloatWindowByID(ctx, "7") }},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.failStart = true
			beforeLoads, beforeStarts, beforeUnloads := provider.loadCount, provider.startCount, provider.unloadCount
			provider.mu.Unlock()
			err := op.run()
			provider.mu.Lock()
			provider.failStart = false
			loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
			provider.mu.Unlock()
			if err == nil || !strings.Contains(err.Error(), "start") {
				t.Fatalf("failed KWin script was accepted: %v", err)
			}
			if loads != beforeLoads+1 || starts != beforeStarts+1 || unloads != beforeUnloads+1 {
				t.Fatalf("script registration leaked: loads %d->%d, starts %d->%d, unloads %d->%d", beforeLoads, loads, beforeStarts, starts, beforeUnloads, unloads)
			}
		})
	}
	desktops, err := manager.ListDesktops(ctx)
	if err != nil || len(desktops) != 2 || !desktops[0].Current {
		t.Fatalf("failed scripts poisoned later desktop read: %+v, %v", desktops, err)
	}
	provider.mu.Lock()
	loads, unloads := provider.loadCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != len(operations)+1 || unloads != loads {
		t.Fatalf("scripts not released after recovery: %d loads / %d unloads", loads, unloads)
	}
}
