package window

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

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
