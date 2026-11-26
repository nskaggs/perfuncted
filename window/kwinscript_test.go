package window

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/dbusutil"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

type syntheticKWinScriptService struct {
	busAddress              string // private D-Bus used by synthetic runtime acceptance tests
	conn                    *dbus.Conn
	mu                      sync.Mutex
	svc                     string
	plugin                  string
	response                string
	listOverride            string // test-only synthetic provider reply for multi-window iteration
	actionOverride          string // test-only script action reply
	loadCount               int
	startCount              int
	unloadCount             int
	loadedPath              string
	loadedScript            string
	noCallback              bool
	failLoad                bool
	failStart               bool
	replaceLoadedScriptPath bool
	closeClientConnOnStart  *dbus.Conn
}

var syntheticKWinServiceName = regexp.MustCompile(`org\.kde\.pflist[0-9]+_[0-9]+`)

func (s *syntheticKWinScriptService) Introspect() (string, *dbus.Error) {
	return "<node><interface name=\"" + kwinScriptIface + "\" /></node>", nil
}
func (s *syntheticKWinScriptService) LoadScript(path, plugin string) (int32, *dbus.Error) {
	s.mu.Lock()
	failLoad := s.failLoad
	s.mu.Unlock()
	if failLoad {
		return 0, dbus.MakeFailedError(errors.New("synthetic script registration denied"))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, dbus.MakeFailedError(err)
	}
	s.mu.Lock()
	replaceLoadedScriptPath := s.replaceLoadedScriptPath
	s.mu.Unlock()
	if replaceLoadedScriptPath {
		if err := os.Remove(path); err != nil {
			return 0, dbus.MakeFailedError(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			return 0, dbus.MakeFailedError(err)
		}
		if err := os.WriteFile(filepath.Join(path, "held"), nil, 0o600); err != nil {
			return 0, dbus.MakeFailedError(err)
		}
	}
	service := syntheticKWinServiceName.FindString(string(data))
	if service == "" {
		return 0, dbus.MakeFailedError(errors.New("missing callback service in script"))
	}
	var response string
	switch {
	case strings.Contains(string(data), "JSON.stringify(rows)"):
		response = `[{"id":"7","title":"Editor","app_id":"editor","class":"Editor","pid":33,"x":-100,"y":20,"width":700,"height":480,"active":true}]`
		if s.listOverride != "" {
			response = s.listOverride
		}
	case strings.Contains(string(data), "JSON.stringify(items)"):
		response = `[{"id":"desktop-1","name":"Work","position":1,"current":true},{"id":"desktop-2","name":"Personal","position":2,"current":false}]`
		if s.listOverride != "" {
			response = s.listOverride
		}
	case strings.Contains(string(data), "JSON.stringify({x:Number(p.x)"):
		response = `{"x":0,"y":0}`
		if s.listOverride != "" {
			response = s.listOverride
		}
	case strings.Contains(string(data), "w ? w.caption"):
		response = "Editor"
	default:
		response = kwinScriptFoundResult
		if s.actionOverride != "" {
			response = s.actionOverride
		}
	}
	s.mu.Lock()
	s.svc = service
	s.plugin = plugin
	s.response = response
	s.loadCount++
	s.loadedPath = path
	s.loadedScript = string(data)
	s.mu.Unlock()
	return 12, nil
}
func (s *syntheticKWinScriptService) Start() *dbus.Error {
	s.mu.Lock()
	svc, response, skip, failStart := s.svc, s.response, s.noCallback, s.failStart
	closeClientConn := s.closeClientConnOnStart
	s.startCount++
	s.mu.Unlock()
	if failStart {
		return dbus.MakeFailedError(errors.New("synthetic script execution denied"))
	}
	if closeClientConn != nil {
		_ = closeClientConn.Close()
		return nil
	}
	if !skip {
		go func() { _ = s.conn.Object(svc, "/").Call(svc+".ReportWindows", 0, response).Err }()
	}
	return nil
}
func (s *syntheticKWinScriptService) UnloadScript(plugin string) *dbus.Error {
	s.mu.Lock()
	s.unloadCount++
	s.mu.Unlock()
	return nil
}
func newSyntheticKWinManager(t *testing.T) (*KWinScriptManager, *syntheticKWinScriptService) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon missing")
	}
	dir := testutil.ShortSocketDir(t)
	addr := "unix:path=" + filepath.Join(dir, "bus")
	daemon := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+addr)
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	var service *dbus.Conn
	var err error
	for i := 0; i < 100; i++ {
		service, err = dbusutil.SessionBusAddressContext(context.Background(), addr)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(kwinScriptSvc, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own fake KWin service: %v %v", reply, err)
	}
	provider := &syntheticKWinScriptService{conn: service, busAddress: addr}
	if lintShadow111 := service.ExportWithMap(provider, map[string]string{
		"LoadScript":   "loadScript",
		"Start":        "start",
		"UnloadScript": "unloadScript",
	}, kwinScriptPath, kwinScriptIface); lintShadow111 != nil {
		t.Fatal(lintShadow111)
	}
	if lintShadow118 := service.Export(provider, kwinScriptPath, "org.freedesktop.DBus.Introspectable"); lintShadow118 != nil {
		t.Fatal(lintShadow118)
	}
	manager, err := NewKWinScriptManagerForBusContext(context.Background(), addr)
	if err != nil {
		t.Fatalf("KWin script manager constructor: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, provider
}

func TestSyntheticKWinScriptListTitleAndCleanup(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wins, err := manager.List(ctx)
	if err != nil || len(wins) != 1 || wins[0].NativeID != "7" || wins[0].Title != "Editor" || wins[0].X != -100 {
		t.Fatalf("KWin window listing %v err=%v", wins, err)
	}
	name, err := manager.ActiveTitle(ctx)
	if err != nil || name != "Editor" {
		t.Fatalf("active title=%q, %v", name, err)
	}
	provider.mu.Lock()
	loads, starts, unloads, path := provider.loadCount, provider.startCount, provider.unloadCount, provider.loadedPath
	provider.mu.Unlock()
	if loads != 2 || starts != 2 || unloads != 2 {
		t.Fatalf("leaked scripts: load=%d start=%d unload=%d", loads, starts, unloads)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary compositor script not removed: %v", err)
	}
}

func TestSyntheticKWinScriptActionsUseStableID(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	actions := []struct {
		name string
		run  func() error
	}{
		{"activate", func() error { return manager.ActivateByID(ctx, "7") }},
		{"move", func() error { return manager.MoveByID(ctx, "7", -500, 25) }},
		{"resize", func() error { return manager.ResizeByID(ctx, "7", 800, 600) }},
		{"close", func() error { return manager.CloseWindowByID(ctx, "7") }},
		{"minimize", func() error { return manager.MinimizeByID(ctx, "7") }},
		{"maximize", func() error { return manager.MaximizeByID(ctx, "7") }},
		{"restore", func() error { return manager.RestoreByID(ctx, "7") }},
	}
	for _, a := range actions {
		if err := a.run(); err != nil {
			t.Fatalf("KWin %s: %v", a.name, err)
		}
	}
	provider.mu.Lock()
	count, unloaded := provider.loadCount, provider.unloadCount
	provider.mu.Unlock()
	if count != len(actions) || unloaded != len(actions) {
		t.Fatalf("script lifecycle %d loaded, %d unloaded", count, unloaded)
	}
}

func TestSyntheticKWinScriptCancelledCallbackCleansLoadedScripts(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	provider.mu.Lock()
	provider.noCallback = true
	provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, err := manager.ActiveTitle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing callback should time out: %v", err)
	}
	provider.mu.Lock()
	loaded, unloaded := provider.loadCount, provider.unloadCount
	provider.mu.Unlock()
	if loaded != 1 || unloaded != 1 {
		t.Fatalf("canceled script leaked: load=%d unload=%d", loaded, unloaded)
	}
}
