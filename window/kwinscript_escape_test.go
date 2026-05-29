package window

import (
	"strconv"
	"strings"
	"testing"
)

func TestKWinJSStringQuotesSpecialCharacters(t *testing.T) {
	input := "Line\\Path\nQuote'\r"
	got := kwinJSString(input)
	want := strconv.Quote(strings.ToLower(input))
	if got != want {
		t.Fatalf("kwinJSString(%q) = %q, want %q", input, got, want)
	}
}

func TestKWinFindWindowScriptUsesQuotedLiteral(t *testing.T) {
	title := "Line\\Path\nQuote'\r"
	literal := kwinJSString(title)
	script := kwinFindWindowScript(literal, "org.kde.pflist1", "w.closeWindow();")

	want := "indexOf(" + literal + ")"
	if !strings.Contains(script, want) {
		t.Fatalf("kwinFindWindowScript missing %q in script:\n%s", want, script)
	}

	legacy := "indexOf('" + strings.ReplaceAll(strings.ToLower(title), "'", "\\'") + "')"
	if strings.Contains(script, legacy) {
		t.Fatalf("kwinFindWindowScript regressed to legacy single-quoted literal: %q", legacy)
	}
	if !strings.Contains(script, kwinScriptErrorPrefix) {
		t.Fatalf("kwinFindWindowScript missing error prefix in script:\n%s", script)
	}
}

func TestKWinWindowActionResult(t *testing.T) {
	if err := kwinWindowActionResult("Firefox", "Firefox"); err != nil {
		t.Fatalf("kwinWindowActionResult success: %v", err)
	}
	if err := kwinWindowActionResult("Firefox", ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("kwinWindowActionResult not found = %v", err)
	}
	if err := kwinWindowActionResult("Firefox", kwinScriptErrorPrefix+"boom"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("kwinWindowActionResult script error = %v", err)
	}
}
