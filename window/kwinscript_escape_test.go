package window

import (
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

func TestKWinActionResultByID(t *testing.T) {
	if err := kwinActionResultByID("opaque", "Firefox"); err != nil {
		t.Fatalf("kwinActionResultByID success: %v", err)
	}
	if err := kwinActionResultByID("opaque", ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("kwinActionResultByID not found = %v", err)
	}
	if err := kwinActionResultByID("opaque", kwinScriptErrorPrefix+"boom"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("kwinActionResultByID script error = %v", err)
	}
}
