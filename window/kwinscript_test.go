package window

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWaitForKWinResultUsesBoundedFallbackTimeout(t *testing.T) {
	_, err := waitForKWinResult(context.Background(), &pfReceiver{ch: make(chan string)}, 31)
	if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "script 31 did not call back") {
		t.Fatalf("waitForKWinResult error = %v, want bounded callback timeout for script 31", err)
	}
}

// KWin's cursor script promises a JSON object with both numeric coordinates.
// A valid position may be (0,0), but absent/null fields are not evidence for it.
func TestKWinCursorRejectsMissingOrNullCompositorCoordinates(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	for _, tc := range []struct{ name, payload string }{
		{"null-object", "null"},
		{"empty-object", "{}"},
		{"missing-x", `{"y":13}`},
		{"missing-y", `{"x":-4}`},
		{"null-x", `{"x":null,"y":13}`},
		{"null-y", `{"x":-4,"y":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.listOverride = tc.payload
			provider.mu.Unlock()
			x, y, err := manager.CursorPosition(context.Background())
			if err == nil {
				t.Fatalf("accepted malformed cursor payload %q as %d,%d", tc.payload, x, y)
			}
		})
	}
}
func TestKWinCursorAcceptsValidZeroAndNegativeCoordinates(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	for _, tc := range []struct {
		payload string
		x, y    int
	}{
		{`{"x":0,"y":0}`, 0, 0},
		{`{"x":-23,"y":107}`, -23, 107},
	} {
		provider.mu.Lock()
		provider.listOverride = tc.payload
		provider.mu.Unlock()
		x, y, err := manager.CursorPosition(context.Background())
		if err != nil || x != tc.x || y != tc.y {
			t.Fatalf("payload=%q got=(%d,%d) err=%v", tc.payload, x, y, err)
		}
	}
}

// Cursor coordinates originate with the compositor, and must not be silently
// rounded, overflowed, or accepted from the wrong JSON type. On any invalid
// coordinate the public readback must return neither stale nor partial data.
func TestKWinCursorPrivateBusRejectsMalformedOrOutOfRangeCoordinates(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, tc := range []struct{ name, reply, fragment string }{
		{"invalid JSON", `{"x":0,`, "cursor position"},
		{"string x", `{"x":"2","y":1}`, "unmarshal"},
		{"boolean y", `{"x":2,"y":true}`, "unmarshal"},
		{"array result", `[2,3]`, "unmarshal"},
		{"x outside platform int", `{"x":1e30,"y":1}`, "cursor x"},
		{"y outside platform int", `{"x":2,"y":-1e30}`, "cursor y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.listOverride = tc.reply
			provider.mu.Unlock()
			x, y, err := manager.CursorPosition(ctx)
			if err == nil || x != 0 || y != 0 || !strings.Contains(err.Error(), tc.fragment) {
				t.Fatalf("cursor payload=%q returned %d,%d err=%v", tc.reply, x, y, err)
			}
		})
	}
	for _, tc := range []struct {
		payload string
		x, y    int
	}{
		{`{"x":-2147483648,"y":2147483647}`, -2147483648, 2147483647},
		{`{"x":2.5,"y":-1.25}`, 2, -1},
		{`{"x":2147483648,"y":-2147483649}`, 2147483648, -2147483649},
	} {
		provider.mu.Lock()
		provider.listOverride = tc.payload
		provider.mu.Unlock()
		x, y, err := manager.CursorPosition(ctx)
		if err != nil || x != tc.x || y != tc.y {
			t.Fatalf("valid cursor position %q = %d,%d err=%v", tc.payload, x, y, err)
		}
	}
	provider.mu.Lock()
	loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != 9 || starts != 9 || unloads != 9 {
		t.Fatalf("cursor scripts leaked: %d loads %d starts %d unloads", loads, starts, unloads)
	}
}

func TestKWinFindByIDScriptUsesQuotedLiteral(t *testing.T) {
	id := "Line\\Path\nQuote'\r"
	literal := strconv.Quote(id)
	script := kwinFindByIDScript(id, "org.kde.pflist1", "w.closeWindow();")

	want := "var targetId = " + literal
	if !strings.Contains(script, want) {
		t.Fatalf("kwinFindByIDScript missing %q in script:\n%s", want, script)
	}

	legacy := "var targetId = '" + strings.ReplaceAll(id, "'", "\\'") + "'"
	if strings.Contains(script, legacy) {
		t.Fatalf("kwinFindByIDScript regressed to legacy single-quoted literal: %q", legacy)
	}
	if !strings.Contains(script, kwinScriptErrorPrefix) {
		t.Fatalf("kwinFindByIDScript missing error prefix in script:\n%s", script)
	}
}

// A window with an empty caption is still found: the script must report the
// located handle, never the caption, or an untitled window looks missing after
// its action already ran.
func TestKWinActionResultByIDReportsUntitledWindowAsFound(t *testing.T) {
	if err := kwinActionResultByID("opaque", kwinScriptFoundResult); err != nil {
		t.Fatalf("kwinActionResultByID untitled window = %v, want success", err)
	}
	if err := kwinActionResultByID("opaque", ""); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("kwinActionResultByID missing window = %v, want ErrWindowNotFound", err)
	}
}

func TestKWinFindByIDScriptReportsFoundWithoutReadingCaption(t *testing.T) {
	script := kwinFindByIDScript("opaque", "org.kde.pflist1", "w.closeWindow();")

	if strings.Contains(script, "w.caption") {
		t.Fatalf("kwinFindByIDScript still reads the caption, which is empty for untitled windows:\n%s", script)
	}
	if !strings.Contains(script, "found = "+strconv.Quote(kwinScriptFoundResult)) {
		t.Fatalf("kwinFindByIDScript does not report the found marker:\n%s", script)
	}
	// runScript resolves the result from a single callDBus callback, so the
	// snippet must keep exactly one.
	if got := strings.Count(script, "callDBus("); got != 1 {
		t.Fatalf("kwinFindByIDScript has %d callDBus calls, want exactly 1:\n%s", got, script)
	}
}

// The action runs after the found marker is set, so a throwing action must
// still surface as a script error rather than as success.
func TestKWinFindByIDScriptReportsActionFailureOverFoundMarker(t *testing.T) {
	script := kwinFindByIDScript("opaque", "org.kde.pflist1", "w.closeWindow();")

	marker := strings.Index(script, "found = "+strconv.Quote(kwinScriptFoundResult))
	action := strings.Index(script, "w.closeWindow();")
	catch := strings.Index(script, "} catch(e) {")
	if marker < 0 || action < 0 || catch < 0 {
		t.Fatalf("kwinFindByIDScript missing expected statements:\n%s", script)
	}
	if marker >= action || action >= catch {
		t.Fatalf("kwinFindByIDScript must set the found marker, then run the action, then catch:\n%s", script)
	}
}

func TestKWinActionResultByID(t *testing.T) {
	if err := kwinActionResultByID("opaque", kwinScriptFoundResult); err != nil {
		t.Fatalf("kwinActionResultByID success: %v", err)
	}
	if err := kwinActionResultByID("opaque", ""); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("kwinActionResultByID not found = %v", err)
	}
	if err := kwinActionResultByID("opaque", kwinScriptErrorPrefix+"boom"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("kwinActionResultByID script error = %v", err)
	}
}

// KWin JSON cannot express NaN or infinity directly, but a decoded or
// adapter-supplied row can. Never turn such compositor geometry into a
// plausible screen coordinate or disclose a partially parsed window.
func TestKWinWindowRowRejectsNonFiniteCoordinatesWithFieldContext(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  float64
		change func(*kwinWindowRow, float64)
	}{
		{"x nan", math.NaN(), func(r *kwinWindowRow, v float64) { r.X = v }},
		{"y negative infinity", math.Inf(-1), func(r *kwinWindowRow, v float64) { r.Y = v }},
		{"width positive infinity", math.Inf(1), func(r *kwinWindowRow, v float64) { r.Width = v }},
		{"height nan", math.NaN(), func(r *kwinWindowRow, v float64) { r.Height = v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := kwinWindowRow{ID: "private-window-27", X: 12, Y: 14, Width: 640, Height: 480}
			tc.change(&row, tc.value)
			got, err := parseKWinWindowRow(row)
			field := strings.Split(tc.name, " ")[0]
			if err == nil || got != (Info{}) || !strings.Contains(err.Error(), row.ID) || !strings.Contains(err.Error(), "geometry "+field+" is not finite") {
				t.Fatalf("invalid KWin coordinate accepted: row=%+v window=%+v err=%v", row, got, err)
			}
		})
	}
}

func TestKWinWindowRowRejectsOutOfRangeOffsetsAndDimensions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*kwinWindowRow)
	}{
		{"x overflow", func(r *kwinWindowRow) { r.X = math.MaxFloat64 }},
		{"y underflow", func(r *kwinWindowRow) { r.Y = -math.MaxFloat64 }},
		{"width overflow", func(r *kwinWindowRow) { r.Width = math.MaxFloat64 }},
		{"height underflow", func(r *kwinWindowRow) { r.Height = -math.MaxFloat64 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := kwinWindowRow{ID: "private-window-27", X: 12, Y: 14, Width: 640, Height: 480}
			tc.change(&row)
			got, err := parseKWinWindowRow(row)
			if err == nil || got != (Info{}) || !strings.Contains(err.Error(), row.ID) || !strings.Contains(err.Error(), "exceeds int range") {
				t.Fatalf("out-of-range KWin geometry accepted: row=%+v window=%+v err=%v", row, got, err)
			}
		})
	}
}

// A revoked request must never reach KWin scripting, regardless of which
// window operation the caller selected. Each operation has a distinct
// wrapper around the script callback and must propagate cancellation.
func TestKWinMutationsDoNotStageScriptsAfterRequestCancellation(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name      string
		operation func(context.Context) error
	}{
		{"activate", func(ctx context.Context) error { return manager.ActivateByID(ctx, "42") }},
		{"move", func(ctx context.Context) error { return manager.MoveByID(ctx, "42", 13, 14) }},
		{"resize", func(ctx context.Context) error { return manager.ResizeByID(ctx, "42", 300, 200) }},
		{"close", func(ctx context.Context) error { return manager.CloseWindowByID(ctx, "42") }},
		{"minimize", func(ctx context.Context) error { return manager.MinimizeByID(ctx, "42") }},
		{"maximize", func(ctx context.Context) error { return manager.MaximizeByID(ctx, "42") }},
		{"fullscreen", func(ctx context.Context) error { return manager.FullscreenByID(ctx, "42") }},
		{"unfullscreen", func(ctx context.Context) error { return manager.UnfullscreenByID(ctx, "42") }},
		{"restore", func(ctx context.Context) error { return manager.RestoreByID(ctx, "42") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.operation(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("revoked request was acknowledged: %v", err)
			}
			provider.mu.Lock()
			loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
			provider.mu.Unlock()
			if loads != 0 || starts != 0 || unloads != 0 {
				t.Fatalf("revoked request reached compositor scripting: loads=%d starts=%d unloads=%d", loads, starts, unloads)
			}
		})
	}
}

func TestKWinWindowParserRejectsExplicitNullGeometry(t *testing.T) {
	for _, field := range []string{"x", "y", "width", "height"} {
		t.Run(field, func(t *testing.T) {
			data := `[{"id":"first","x":9,"y":7,"width":12,"height":15},{"id":"second","` + field + `":null}]`
			windows, err := parseKWinWindowList(data)
			if err == nil {
				t.Fatalf("accepted JSON null %s as coordinate zero: %+v", field, windows)
			}
			if !strings.Contains(err.Error(), field) {
				t.Fatalf("error does not identify malformed %s: %v", field, err)
			}
		})
	}
}

func TestKWinWindowParserAllowsOmittedGeometryAndExplicitZero(t *testing.T) {
	windows, err := parseKWinWindowList(`[{"id":"omitted"},{"id":"origin","x":0,"y":0,"width":0,"height":0}]`)
	if err != nil || len(windows) != 2 || windows[0].X != 0 || windows[1].W != 0 {
		t.Fatalf("valid zero geometry failed: %+v err=%v", windows, err)
	}
}

// KWin's script explicitly serializes arrays, including [] when none exist.
// JSON null has no corresponding successful compositor list response and must
// not be silently treated as an empty desktop or window inventory.
func TestKWinListParsersRejectNullCompositorPayloads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parse func(string) error
	}{
		{"desktops", func(data string) error { _, err := parseKWinDesktops(data); return err }},
		{"windows", func(data string) error { _, err := parseKWinWindowList(data); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.parse("null"); err == nil {
				t.Fatal("accepted null KWin response as an authoritative empty list")
			}
		})
	}
}

func TestKWinWindowListRejectsExplicitNullState(t *testing.T) {
	for _, field := range []string{
		"pid", "active", "minimized", "maximized", "fullscreen", "keep_above", "keep_below",
		"shaded", "shadeable", "on_all_desktops", "desktop_ids", "tiled",
	} {
		t.Run(field, func(t *testing.T) {
			data := `[{"id":"good","active":true},{"id":"bad","` + field + `":null}]`
			list, err := parseKWinWindowList(data)
			if err == nil {
				t.Fatalf("accepted null compositor state %s: %+v", field, list)
			}
			if !strings.Contains(err.Error(), field) {
				t.Fatalf("error must identify %s: %v", field, err)
			}
		})
	}
}

func TestKWinDesktopListRejectsExplicitNullCurrent(t *testing.T) {
	data := `[{"id":"first","position":1,"current":true},{"id":"second","position":2,"current":null}]`
	ds, err := parseKWinDesktops(data)
	if err == nil {
		t.Fatalf("accepted null desktop current flag: %+v", ds)
	}
}

func TestKWinStateAllowsOmittedFields(t *testing.T) {
	list, err := parseKWinWindowList(`[{"id":"first","active":true},{"id":"second"}]`)
	if err != nil || len(list) != 2 || !list[0].Active {
		t.Fatalf("omitted fields rejected: %+v err=%v", list, err)
	}
	ds, err := parseKWinDesktops(`[{"id":"first","position":1,"current":true},{"id":"second","position":2}]`)
	if err != nil || len(ds) != 2 {
		t.Fatalf("omitted current rejected: %+v err=%v", ds, err)
	}
}

func TestKWinWindowListRejectsContradictoryCompositorIdentity(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"two active windows", `[{"id":"one","active":true},{"id":"two","active":true}]`},
		{"negative process id", `[{"id":"one","pid":-1}]`},
		{"negative second process id", `[{"id":"one","pid":0},{"id":"two","pid":-54}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseKWinWindowList(tc.payload)
			if err == nil {
				t.Fatalf("accepted contradictory compositor window snapshot: %+v", got)
			}
		})
	}
}
func TestKWinWindowListAllowsNoActiveWindowAndUnknownPID(t *testing.T) {
	got, err := parseKWinWindowList(`[{"id":"one","pid":0},{"id":"two","pid":17}]`)
	if err != nil || len(got) != 2 || got[0].Active || got[1].Active {
		t.Fatalf("rejected valid unfocused snapshot: %+v err=%v", got, err)
	}
}

// Temporary KWin scripts carry an authenticated callback bus name. Refuse
// creation when private storage is unavailable instead of running the builder
// or publishing a script at an unintended location.
func TestKWinScriptTransportRejectsUnusableTemporaryDirectory(t *testing.T) {
	root := t.TempDir()
	notDirectory := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(notDirectory, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", notDirectory)
	called := false
	path, err := writeKWinScript(func(string) string {
		called = true
		return "callDBus('untrusted');"
	}, "org.example.private")
	if err == nil || !strings.Contains(err.Error(), "temp file") || path != "" {
		t.Fatalf("unusable temp storage returned path %q, err %v", path, err)
	}
	if called {
		t.Fatal("script builder ran without private temporary storage")
	}
	data, err := os.ReadFile(notDirectory)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("unusable storage altered: contents %q, error %v", data, err)
	}
}

// The written file must contain only the generated JS and remain private; it
// is later loaded by the privileged KWin scripting D-Bus interface.
func TestKWinScriptTransportWritesPrivateExactContent(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const service = "org.kde.pflist123_456"
	want := "callDBus('" + service + "', '/', '" + service + "', 'ReportWindows', 'a\\\\b');"
	path, err := writeKWinScript(func(got string) string {
		if got != service {
			t.Fatalf("builder service = %q, want %q", got, service)
		}
		return want
	}, service)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("script permissions = %o, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("script bytes = %q err=%v, want %q", data, err, want)
	}
}

func TestParseKWinWindowListPreservesWindowIDs(t *testing.T) {
	data := `[ {"id":"42","title":"Window One\twith tab","app_id":"org.app.one","class":"AppOne","pid":101,"x":10,"y":20,"width":300.5,"height":400},
{"id":"0x2b","title":"Window Two","app_id":"org.app.two","class":"AppTwo","pid":202,"x":30.9,"y":40,"width":500,"height":600.1},
{"id":"{bfe19e8e-18f4-4d48}","title":"Window Three","app_id":"org.app.three","class":"AppThree","pid":303,"x":1,"y":2,"width":3,"height":4} ]`

	got, err := parseKWinWindowList(data)
	if err != nil {
		t.Fatalf("parseKWinWindowList: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("parseKWinWindowList len = %d, want 3", len(got))
	}
	if got[0].ID != 42 {
		t.Fatalf("first ID = %d, want 42", got[0].ID)
	}
	if got[0].Title != "Window One\twith tab" || got[0].AppID != "org.app.one" || got[0].Class != "AppOne" || got[0].PID != 101 {
		t.Fatalf("first window = %+v", got[0])
	}
	if got[0].W != 300 || got[0].H != 400 {
		t.Fatalf("first geometry = %dx%d, want 300x400", got[0].W, got[0].H)
	}
	if got[1].ID != 0x2b {
		t.Fatalf("second ID = %d, want %d", got[1].ID, 0x2b)
	}
	if got[1].X != 30 || got[1].H != 600 {
		t.Fatalf("second geometry = %+v", got[1])
	}
	if got[2].NativeID != "{bfe19e8e-18f4-4d48}" ||
		got[2].StableID() != "{bfe19e8e-18f4-4d48}" {
		t.Fatalf("third stable ID = %q", got[2].StableID())
	}
}

func TestParseKWinWindowListRejectsMalformedJSON(t *testing.T) {
	if _, err := parseKWinWindowList("not-json"); err == nil {
		t.Fatal("parseKWinWindowList accepted malformed JSON")
	}
}

func TestParseKWinWindowListRejectsInvalidGeometry(t *testing.T) {
	for _, data := range []string{
		`[{"id":"1","width":1e20,"height":10}]`,
		`[{"id":"1","width":-1,"height":10}]`,
	} {
		if _, err := parseKWinWindowList(data); err == nil {
			t.Fatalf("parseKWinWindowList accepted invalid geometry %s", data)
		}
	}
}

func TestParseKWinWindowListUsesJSONEscaping(t *testing.T) {
	data, err := json.Marshal([]kwinWindowRow{{ID: "17", Title: "line 1\nline 2", AppID: "org.example"}})
	if err != nil {
		t.Fatalf("marshal test row: %v", err)
	}
	got, err := parseKWinWindowList(string(data))
	if err != nil {
		t.Fatalf("parseKWinWindowList: %v", err)
	}
	if len(got) != 1 || got[0].Title != "line 1\nline 2" {
		t.Fatalf("parsed windows = %#v, want preserved title", got)
	}
}

func TestKWinRunScriptCanceledContextShortCircuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	_, err := (&KWinScriptManager{}).runScript(ctx, func(string) string {
		called = true
		return ""
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runScript error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("runScript called buildJS after context cancellation")
	}
}

func TestKWinRunScriptRequiresInitializedBackend(t *testing.T) {
	_, err := (&KWinScriptManager{}).runScript(context.Background(), func(string) string { return "" })
	if err == nil || err.Error() != "window/kwinscript: backend not initialised" {
		t.Fatalf("runScript error = %v, want backend not initialised", err)
	}
}

func TestKWinResultContextPreservesPolicyDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	waitCtx, waitCancel := kwinResultContext(ctx)
	defer waitCancel()
	got, ok := waitCtx.Deadline()
	if !ok || !got.Equal(deadline) {
		t.Fatalf("KWin callback deadline = %v, present=%v, want caller policy deadline %v", got, ok, deadline)
	}
}

func TestWaitForKWinResultPreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForKWinResult(ctx, &pfReceiver{ch: make(chan string)}, 7)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForKWinResult error = %v, want context.Canceled", err)
	}
}
