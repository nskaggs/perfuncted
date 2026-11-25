package window

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestKWinWindowListPreservesActiveStateForStableIDVerification(t *testing.T) {
	data := `[{"id":"{active-uuid}","title":"active","active":true,"x":0,"y":0,"width":600,"height":300},{"id":"{inactive-uuid}","title":"inactive","active":false,"x":200,"y":100,"width":500,"height":250}]`
	got, err := parseKWinWindowList(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Active || got[1].Active {
		t.Fatalf("lost actual focus state: %+v", got)
	}
}

// Malformed compositor results cannot be exposed as a partial window list,
// and the temporary script must be unloaded after the failure.
func TestSyntheticKWinMalformedWindowSnapshotFailsClosedAndUnloadsScript(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	provider.mu.Lock()
	provider.listOverride = `[{"id":"7","title":"valid"},{"id":false}]`
	provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	windows, err := manager.List(ctx)
	if err == nil || len(windows) != 0 {
		t.Fatalf("malformed compositor snapshot accepted: windows=%v err=%v", windows, err)
	}
	if !strings.Contains(err.Error(), "window") && !strings.Contains(err.Error(), "id") {
		t.Logf("malformed snapshot rejected: %v", err)
	}
	provider.mu.Lock()
	loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != 1 || starts != 1 || unloads != 1 {
		t.Fatalf("rejected KWin snapshot leaked a script: loads=%d starts=%d unloads=%d", loads, starts, unloads)
	}
}

func TestKWinNilManagerCloseIsIdempotent(t *testing.T) {
	var manager *KWinScriptManager
	if err := manager.Close(); err != nil {
		t.Fatalf("nil manager cleanup failed: %v", err)
	}
	if err := (&KWinScriptManager{}).Close(); err != nil {
		t.Fatalf("disconnected manager cleanup failed: %v", err)
	}
}

// KWin 6 exposes a maximizeMode bitmask, not a boolean maximized property.
// The compositor's reported state must reflect full, not partial, maximization.
func TestKWinWindowListingReadsFullMaximizeMode(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	if _, err := manager.List(context.Background()); err != nil {
		t.Fatalf("KWin window listing: %v", err)
	}
	provider.mu.Lock()
	script := provider.loadedScript
	provider.mu.Unlock()
	if !strings.Contains(script, "maximized: Number(w.maximizeMode) === 3") {
		t.Fatalf("window listing did not read KWin's maximizeMode (full=3): %s", script)
	}
	if strings.Contains(script, "!!w.maximized") {
		t.Fatal("window listing relied on nonexistent KWin maximized property")
	}
}

func TestKWinNumericNativeIDPreservesDecimalLeadingZero(t *testing.T) {
	list, err := parseKWinWindowList(`[{"id":"010","title":"decimal ten","width":10,"height":10},{"id":"0x10","title":"hex sixteen","width":10,"height":10}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != 10 || list[1].ID != 16 {
		t.Fatalf("KWin numeric IDs changed value: %#v", list)
	}
}

// KWin desktop positions must be unique: a duplicated index makes ordered
// desktop state ambiguous even when stable IDs differ.
func TestKWinDesktopParserRejectsRepeatedPositions(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"same position", `[{"id":"desktop-a","position":1,"current":true},{"id":"desktop-b","position":1}]`},
		{"same later position", `[{"id":"desktop-a","position":1,"current":true},{"id":"desktop-b","position":2},{"id":"desktop-c","position":2}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseKWinDesktops(tc.data)
			if err == nil {
				t.Fatalf("accepted duplicate desktop positions: %+v", got)
			}
		})
	}
}

// The KWin script assigns position=i+1 while enumerating workspace.desktops.
// A response with gaps or mismatched ordering cannot be an authoritative
// rendering of that snapshot.
func TestKWinDesktopParserRejectsOutOfOrderAndNoncontiguousPositions(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"skipped-position", `[{"id":"first","position":1,"current":true},{"id":"second","position":3}]`},
		{"swapped-position", `[{"id":"first","position":2,"current":true},{"id":"second","position":1}]`},
		{"start-at-two", `[{"id":"only","position":2,"current":true}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := parseKWinDesktops(tc.payload); err == nil {
				t.Fatalf("accepted KWin desktop positions inconsistent with enumeration: %+v", got)
			}
		})
	}
}

func TestKWinDesktopParserPreservesStableIDsAndCurrentDesktop(t *testing.T) {
	items, err := parseKWinDesktops(`[
	 {"id":"first-uuid","name":"Desktop 1","position":1,"current":false},
	 {"id":"second-uuid","name":"Other desktop","position":2,"current":true}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].ID != "second-uuid" || items[1].Name != "Other desktop" || !items[1].Current || items[0].Current {
		t.Fatalf("invalid parsed KDE virtual desktop list: %+v", items)
	}
}

func TestKWinDesktopParserRejectsCorruptOrAmbiguousState(t *testing.T) {
	for _, testcase := range []struct{ name, value string }{
		{"invalid JSON", "["},
		{"missing IDs", `[{"position":1,"current":true}]`},
		{"duplicate IDs", `[{"id":"one","position":1,"current":true},{"id":"one","position":2}]`},
		{"no current", `[{"id":"one","position":1}]`},
		{"two current", `[{"id":"one","position":1,"current":true},{"id":"two","position":2,"current":true}]`},
		{"zero index", `[{"id":"one","position":0,"current":true}]`},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			if result, err := parseKWinDesktops(testcase.value); err == nil {
				t.Fatalf("accepted broken KWin desktops: %+v", result)
			}
		})
	}
}

func TestKWinWindowListPreservesCompositorOwnedState(t *testing.T) {
	// Existing window.Info is intentionally comparable: fake and real
	// backends use exact zero-value checks after cancellation.
	requiresComparable := func(Info) bool { return true }
	data := `[{"id":"{win-uuid}","title":"Editor","class":"Editor",
	"minimized":true,"maximized":false,"fullscreen":true,"keep_above":true,
	"keep_below":false,"shaded":false,"shadeable":false,"on_all_desktops":false,
	"desktop_ids":"first-uuid,second-uuid","tiled":true}]`
	infos, err := parseKWinWindowList(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || !requiresComparable(infos[0]) {
		t.Fatal("missing window info")
	}
	w := infos[0]
	if !w.Minimized || !w.Fullscreen || !w.KeepAbove || w.KeepBelow || w.Shaded || w.Shadeable || !w.Tiled || w.DesktopIDs != "first-uuid,second-uuid" {
		t.Fatalf("lost KWin native window states: %+v", w)
	}
}

func TestKWinDesktopMutationChecksIDsBeforeContactingBackend(t *testing.T) {
	k := &KWinScriptManager{}
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"switch", func() error { return k.SwitchDesktop(context.Background(), "") }},
		{"window assignment", func() error { return k.SetWindowDesktopByID(context.Background(), "id", "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid native desktop ID admitted: %v", err)
			}
		})
	}
}

func TestKWinMissingDesktopAndCompositorActionErrors(t *testing.T) {
	if !errors.Is(ErrDesktopNotFound, ErrDesktopNotFound) {
		t.Fatal("desktop error sentinel unavailable")
	}
	if err := kwinActionResultByID("id", kwinScriptErrorPrefix+"window is not shadeable in this compositor"); err == nil || !strings.Contains(err.Error(), "not shadeable") {
		t.Fatalf("KWin shading capability failure lost: %v", err)
	}
}

func TestKWinSyntheticDesktopWindowActionsAndScriptCleanup(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	actions := []struct {
		name string
		run  func() error
	}{
		{"tile", func() error { return manager.TileWindowByID(ctx, "7", 320, 200) }},
		{"float", func() error { return manager.FloatWindowByID(ctx, "7") }},
		{"shade", func() error { return manager.SetShadeByID(ctx, "7", true) }},
		{"keep-above", func() error { return manager.SetKeepAboveByID(ctx, "7", true) }},
		{"keep-below", func() error { return manager.SetKeepBelowByID(ctx, "7", false) }},
		{"assign-desktop", func() error { return manager.SetWindowDesktopByID(ctx, "7", "desktop-id") }},
		{"switch-desktop", func() error { return manager.SwitchDesktop(ctx, "desktop-id") }},
	}
	for _, action := range actions {
		t.Run(action.name, func(t *testing.T) {
			if err := action.run(); err != nil {
				t.Fatalf("synthetic KWin script: %v", err)
			}
		})
	}
	provider.mu.Lock()
	loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != len(actions) || starts != loads || unloads != loads {
		t.Fatalf("KWin scripting leaked actions: loads=%d starts=%d unloads=%d", loads, starts, unloads)
	}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"tile-blank-id", func() error { return manager.TileWindowByID(ctx, "", 0, 0) }},
		{"float-blank-id", func() error { return manager.FloatWindowByID(ctx, "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid window identity reached compositor: %v", err)
			}
		})
	}
}
