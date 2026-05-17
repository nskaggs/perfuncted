package window

import (
	"reflect"
	"testing"
)

// ── parseMatchBool ────────────────────────────────────────────────────────────

func TestParseMatchBool_True(t *testing.T) {
	for _, v := range []string{"1", "t", "true", "yes", "on", "TRUE", "Yes"} {
		b, err := parseMatchBool(v)
		if err != nil {
			t.Errorf("parseMatchBool(%q) error = %v", v, err)
		}
		if !b {
			t.Errorf("parseMatchBool(%q) = false, want true", v)
		}
	}
}

func TestParseMatchBool_False(t *testing.T) {
	for _, v := range []string{"0", "f", "false", "no", "off", "FALSE"} {
		b, err := parseMatchBool(v)
		if err != nil {
			t.Errorf("parseMatchBool(%q) error = %v", v, err)
		}
		if b {
			t.Errorf("parseMatchBool(%q) = true, want false", v)
		}
	}
}

func TestParseMatchBool_EmptyIsTrue(t *testing.T) {
	b, err := parseMatchBool("")
	if err != nil {
		t.Fatalf("parseMatchBool(\"\") error = %v", err)
	}
	if !b {
		t.Fatal("parseMatchBool(\"\") = false, want true")
	}
}

func TestParseMatchBool_Invalid(t *testing.T) {
	_, err := parseMatchBool("bogus")
	if err == nil {
		t.Fatal("parseMatchBool(\"bogus\") expected error, got nil")
	}
}

// ── tokenizeMatchSpec ─────────────────────────────────────────────────────────

func TestTokenizeMatchSpec_EscapedChar(t *testing.T) {
	// Backslash inside quotes is an escape character.
	tokens, err := tokenizeMatchSpec(`"hello\"world"`)
	if err != nil {
		t.Fatalf("tokenizeMatchSpec error = %v", err)
	}
	if len(tokens) != 1 || tokens[0] != `hello"world` {
		t.Fatalf("tokens = %v, want [%s]", tokens, `hello"world`)
	}
}

func TestTokenizeMatchSpec_SingleQuoted(t *testing.T) {
	tokens, err := tokenizeMatchSpec("'hello world'")
	if err != nil {
		t.Fatalf("tokenizeMatchSpec error = %v", err)
	}
	if len(tokens) != 1 || tokens[0] != "hello world" {
		t.Fatalf("tokens = %v, want [hello world]", tokens)
	}
}

func TestTokenizeMatchSpec_CommaSeparated(t *testing.T) {
	tokens, err := tokenizeMatchSpec("foo,bar,baz")
	if err != nil {
		t.Fatalf("tokenizeMatchSpec error = %v", err)
	}
	want := []string{"foo", "bar", "baz"}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
}

func TestTokenizeMatchSpec_TrailingBackslash(t *testing.T) {
	// A trailing backslash outside quotes is treated as literal backslash.
	tokens, err := tokenizeMatchSpec(`hello\`)
	if err != nil {
		t.Fatalf("tokenizeMatchSpec error = %v", err)
	}
	if len(tokens) != 1 || tokens[0] != `hello\` {
		t.Fatalf("tokens = %v, want [hello\\]", tokens)
	}
}

func TestTokenizeMatchSpec_UnterminatedQuote(t *testing.T) {
	_, err := tokenizeMatchSpec(`"unterminated`)
	if err == nil {
		t.Fatal("expected error for unterminated quote")
	}
}

// ── applyMatchState ───────────────────────────────────────────────────────────

func TestApplyMatchState_AllFields(t *testing.T) {
	tests := []struct {
		raw        string
		wantActive *bool
		wantMin    *bool
		wantMax    *bool
		wantFS     *bool
		wantVis    bool
	}{
		{"active", boolPtr(true), nil, nil, nil, false},
		{"-active", boolPtr(false), nil, nil, nil, false},
		{"minimized", nil, boolPtr(true), nil, nil, false},
		{"-minimized", nil, boolPtr(false), nil, nil, false},
		{"maximized", nil, nil, boolPtr(true), nil, false},
		{"-maximized", nil, nil, boolPtr(false), nil, false},
		{"fullscreen", nil, nil, nil, boolPtr(true), false},
		{"-fullscreen", nil, nil, nil, boolPtr(false), false},
		{"visible-only", nil, nil, nil, nil, true},
		{"-visible-only", nil, nil, nil, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			var m Match
			if err := applyMatchState(&m, tc.raw); err != nil {
				t.Fatalf("applyMatchState(%q) error = %v", tc.raw, err)
			}
			if !boolPtrEq(m.Active, tc.wantActive) {
				t.Errorf("Active = %v, want %v", m.Active, tc.wantActive)
			}
			if !boolPtrEq(m.Minimized, tc.wantMin) {
				t.Errorf("Minimized = %v, want %v", m.Minimized, tc.wantMin)
			}
			if !boolPtrEq(m.Maximized, tc.wantMax) {
				t.Errorf("Maximized = %v, want %v", m.Maximized, tc.wantMax)
			}
			if !boolPtrEq(m.Fullscreen, tc.wantFS) {
				t.Errorf("Fullscreen = %v, want %v", m.Fullscreen, tc.wantFS)
			}
			if m.VisibleOnly != tc.wantVis {
				t.Errorf("VisibleOnly = %v, want %v", m.VisibleOnly, tc.wantVis)
			}
		})
	}
}

func TestApplyMatchState_UnknownState(t *testing.T) {
	var m Match
	if err := applyMatchState(&m, "bogusstate"); err == nil {
		t.Fatal("applyMatchState expected error for unknown state")
	}
}

func boolPtrEq(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// ── ParseMatchSpec — additional coverage ─────────────────────────────────────

func TestParseMatchSpec_BoolKeywords(t *testing.T) {
	tests := []struct {
		spec string
		want Match
	}{
		{"active", Match{Active: boolPtr(true)}},
		{"minimized", Match{Minimized: boolPtr(true)}},
		{"maximized", Match{Maximized: boolPtr(true)}},
		{"fullscreen", Match{Fullscreen: boolPtr(true)}},
		{"visible", Match{VisibleOnly: true}},
		{"visible-only", Match{VisibleOnly: true}},
	}
	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			got, err := ParseMatchSpec(tc.spec)
			if err != nil {
				t.Fatalf("ParseMatchSpec(%q) error = %v", tc.spec, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseMatchSpec(%q) = %+v, want %+v", tc.spec, got, tc.want)
			}
		})
	}
}

func TestParseMatchSpec_BoolValues(t *testing.T) {
	tests := []struct {
		spec       string
		wantActive *bool
		wantMin    *bool
		wantMax    *bool
		wantFS     *bool
		wantVis    bool
	}{
		{"active=true", boolPtr(true), nil, nil, nil, false},
		{"active=false", boolPtr(false), nil, nil, nil, false},
		{"minimized=1", nil, boolPtr(true), nil, nil, false},
		{"minimized=0", nil, boolPtr(false), nil, nil, false},
		{"maximized=yes", nil, nil, boolPtr(true), nil, false},
		{"maximized=no", nil, nil, boolPtr(false), nil, false},
		{"fullscreen=on", nil, nil, nil, boolPtr(true), false},
		{"fullscreen=off", nil, nil, nil, boolPtr(false), false},
		{"visible-only=true", nil, nil, nil, nil, true},
		{"visible-only=false", nil, nil, nil, nil, false},
		{"visible=true", nil, nil, nil, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			got, err := ParseMatchSpec(tc.spec)
			if err != nil {
				t.Fatalf("ParseMatchSpec(%q) error = %v", tc.spec, err)
			}
			if !boolPtrEq(got.Active, tc.wantActive) {
				t.Errorf("Active = %v, want %v", got.Active, tc.wantActive)
			}
			if !boolPtrEq(got.Minimized, tc.wantMin) {
				t.Errorf("Minimized = %v, want %v", got.Minimized, tc.wantMin)
			}
			if !boolPtrEq(got.Maximized, tc.wantMax) {
				t.Errorf("Maximized = %v, want %v", got.Maximized, tc.wantMax)
			}
			if !boolPtrEq(got.Fullscreen, tc.wantFS) {
				t.Errorf("Fullscreen = %v, want %v", got.Fullscreen, tc.wantFS)
			}
			if got.VisibleOnly != tc.wantVis {
				t.Errorf("VisibleOnly = %v, want %v", got.VisibleOnly, tc.wantVis)
			}
		})
	}
}

func TestParseMatchSpec_AppAliases(t *testing.T) {
	tests := []struct {
		spec  string
		appID string
	}{
		{"app=org.example", "org.example"},
		{"appid=org.test", "org.test"},
		{"app_id=org.foo", "org.foo"},
	}
	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			got, err := ParseMatchSpec(tc.spec)
			if err != nil {
				t.Fatalf("ParseMatchSpec(%q) error = %v", tc.spec, err)
			}
			if got.AppID != tc.appID {
				t.Fatalf("AppID = %q, want %q", got.AppID, tc.appID)
			}
		})
	}
}

func TestParseMatchSpec_ClassKey(t *testing.T) {
	got, err := ParseMatchSpec("class=Terminal")
	if err != nil {
		t.Fatalf("ParseMatchSpec error = %v", err)
	}
	if got.Class != "Terminal" {
		t.Fatalf("Class = %q, want Terminal", got.Class)
	}
}

func TestParseMatchSpec_TitleSubstringKey(t *testing.T) {
	got, err := ParseMatchSpec("title~=editor")
	if err != nil {
		t.Fatalf("ParseMatchSpec error = %v", err)
	}
	if got.TitleContains != "editor" {
		t.Fatalf("TitleContains = %q, want editor", got.TitleContains)
	}
}

func TestParseMatchSpec_MultipleTitleSubstrings(t *testing.T) {
	_, err := ParseMatchSpec("hello world")
	if err == nil {
		t.Fatal("expected error for multiple title substring terms")
	}
}

func TestParseMatchSpec_UnknownKey(t *testing.T) {
	_, err := ParseMatchSpec("boguskey=value")
	if err == nil {
		t.Fatal("expected error for unknown match key")
	}
}

func TestParseMatchSpec_InvalidBoolValue(t *testing.T) {
	for _, spec := range []string{"active=bogus", "minimized=nope", "maximized=invalid", "fullscreen=junk", "visible-only=bad"} {
		t.Run(spec, func(t *testing.T) {
			if _, err := ParseMatchSpec(spec); err == nil {
				t.Fatalf("ParseMatchSpec(%q) expected error, got nil", spec)
			}
		})
	}
}

func TestParseMatchSpec_ColonSyntax(t *testing.T) {
	// title:value is the colon-separator form
	got, err := ParseMatchSpec("title:MyTitle")
	if err != nil {
		t.Fatalf("ParseMatchSpec error = %v", err)
	}
	if got.TitleExact != "MyTitle" {
		t.Fatalf("TitleExact = %q, want MyTitle", got.TitleExact)
	}
}

// ── Match.Matches — branches not yet covered ──────────────────────────────────

func TestMatch_Matches_IDMismatch(t *testing.T) {
	info := Info{ID: 5}
	if (Match{ID: uint64Ptr(99)}).Matches(info) {
		t.Fatal("expected false for ID mismatch")
	}
}

func TestMatch_Matches_ActiveMismatch(t *testing.T) {
	info := Info{Active: false}
	if (Match{Active: boolPtr(true)}).Matches(info) {
		t.Fatal("expected false when Active mismatch")
	}
}

func TestMatch_Matches_MinimizedMismatch(t *testing.T) {
	info := Info{Minimized: false}
	if (Match{Minimized: boolPtr(true)}).Matches(info) {
		t.Fatal("expected false when Minimized mismatch")
	}
}

func TestMatch_Matches_MaximizedMismatch(t *testing.T) {
	info := Info{Maximized: false}
	if (Match{Maximized: boolPtr(true)}).Matches(info) {
		t.Fatal("expected false when Maximized mismatch")
	}
}

func TestMatch_Matches_FullscreenMismatch(t *testing.T) {
	info := Info{Fullscreen: false}
	if (Match{Fullscreen: boolPtr(true)}).Matches(info) {
		t.Fatal("expected false when Fullscreen mismatch")
	}
}

func TestMatch_Matches_TitleExactMismatch(t *testing.T) {
	info := Info{Title: "Foo Bar"}
	if (Match{TitleExact: "Baz"}).Matches(info) {
		t.Fatal("expected false for TitleExact mismatch")
	}
}

func TestMatch_Matches_AppIDMismatch(t *testing.T) {
	info := Info{AppID: "org.example"}
	if (Match{AppID: "org.other"}).Matches(info) {
		t.Fatal("expected false for AppID mismatch")
	}
}

func TestMatch_Matches_ClassMismatch(t *testing.T) {
	info := Info{Class: "Firefox"}
	if (Match{Class: "Chrome"}).Matches(info) {
		t.Fatal("expected false for Class mismatch")
	}
}

func TestMatch_Matches_PIDMismatch(t *testing.T) {
	info := Info{PID: 100}
	if (Match{PID: int32Ptr(200)}).Matches(info) {
		t.Fatal("expected false for PID mismatch")
	}
}

func TestMatch_Matches_TitleContainsMismatch(t *testing.T) {
	info := Info{Title: "Hello World"}
	if (Match{TitleContains: "zzz"}).Matches(info) {
		t.Fatal("expected false for TitleContains mismatch")
	}
}

func TestMatch_Matches_EmptyMatchAll(t *testing.T) {
	info := Info{ID: 42, Title: "Anything"}
	if !(Match{}).Matches(info) {
		t.Fatal("empty Match should match any window")
	}
}

func TestCompileMatch_Matches(t *testing.T) {
	active := true
	info := Info{
		ID:        7,
		Title:     "Exact Title",
		AppID:     "org.example",
		Class:     "Example",
		PID:       42,
		Active:    true,
		Minimized: false,
	}
	match := Match{
		TitleContains: "exact",
		AppID:         "org.example",
		Class:         "example",
		Active:        &active,
	}
	compiled := CompileMatch(match)
	if !compiled.Matches(info) {
		t.Fatal("compiled matcher should match the same window as Match.Matches")
	}
	if !match.Matches(info) {
		t.Fatal("Match.Matches should match the same window")
	}
}

// ── Match.String — all fields ─────────────────────────────────────────────────

func TestMatchString_AllFields(t *testing.T) {
	pid := int32(42)
	id := uint64(7)
	m := Match{
		TitleExact:    "Exact",
		TitleContains: "sub",
		AppID:         "org.example",
		Class:         "MyClass",
		PID:           &pid,
		ID:            &id,
		Active:        boolPtr(true),
		Minimized:     boolPtr(false),
		Maximized:     boolPtr(true),
		Fullscreen:    boolPtr(false),
		VisibleOnly:   true,
	}
	s := m.String()
	for _, want := range []string{
		"title=", "title~=", "app_id=", "class=", "pid=42", "id=7",
		"active=true", "minimized=false", "maximized=true", "fullscreen=false", "visible-only",
	} {
		if !containsStr(s, want) {
			t.Errorf("Match.String() missing %q; got %q", want, s)
		}
	}
}

func TestMatchString_Empty(t *testing.T) {
	s := (Match{}).String()
	if s != "<any window>" {
		t.Fatalf("Match{}.String() = %q, want <any window>", s)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
