package window

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

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
