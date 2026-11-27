package window

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The bounded cache can already be full from unrelated tests. Exercise a
// deterministic fresh cache and restore the prior cache on test cleanup.
func isolatedCompiledMatchCache(t *testing.T) {
	t.Helper()
	compiledMatchCacheMu.Lock()
	old := compiledMatchCache
	compiledMatchCache = make(map[matchCacheKey]Matcher)
	compiledMatchCacheMu.Unlock()
	t.Cleanup(func() {
		compiledMatchCacheMu.Lock()
		compiledMatchCache = old
		compiledMatchCacheMu.Unlock()
	})
}

func TestCompileMatchSnapshotsMutablePredicates(t *testing.T) {
	isolatedCompiledMatchCache(t)
	pid := int32(1201)
	active := true
	id := uint64(10)
	title := "perfuncted-predicate-snapshot-unique"
	compiled := CompileMatch(Match{TitleContains: title, PID: &pid, ID: &id, Active: &active})
	before := Info{Title: title, PID: 1201, ID: 10, Active: true}
	after := Info{Title: title, PID: 1202, ID: 11, Active: false}
	if !compiled.Matches(before) {
		t.Fatal("baseline matcher rejected original predicates")
	}
	pid, active, id = 1202, false, 11
	if !compiled.Matches(before) || compiled.Matches(after) {
		t.Fatal("compiled matcher changed when caller modified source predicate pointers")
	}
	// A fresh matcher for the original key must still use its original values.
	matching := CompileMatch(Match{TitleContains: title, PID: ptrInt32(1201), ID: ptrUint64(10), Active: ptrBool(true)})
	if !matching.Matches(before) || matching.Matches(after) {
		t.Fatal("cache retained mutated predicate values")
	}
}

func TestCompileMatchReturnedMatchersDoNotMutateSharedCache(t *testing.T) {
	isolatedCompiledMatchCache(t)
	title := "perfuncted-cache-isolation-unique"
	returned := CompileMatch(Match{TitleContains: title, PID: ptrInt32(1734), Minimized: ptrBool(false)})
	*returned.PID = 3333
	*returned.Minimized = true
	other := CompileMatch(Match{TitleContains: title, PID: ptrInt32(1734), Minimized: ptrBool(false)})
	if !other.Matches(Info{Title: title, PID: 1734, Minimized: false}) {
		t.Fatal("mutating a returned matcher contaminated the compiled cache")
	}
}

func ptrInt32(v int32) *int32    { return &v }
func ptrUint64(v uint64) *uint64 { return &v }
func ptrBool(v bool) *bool       { return &v }

// Under a simultaneous cold-key stampede a second caller can miss the read
// cache before another publisher acquires the writer lock. Every result must
// still own its predicates, and only one fully initialized entry may persist.
func TestCompileMatchContendedColdPublicationKeepsPredicateIsolation(t *testing.T) {
	isolatedCompiledMatchCache(t)
	const callers = 96
	// Nontrivial normalization keeps workers in the pre-publication phase
	// long enough to exercise actual competing cache misses under -race too.
	title := strings.Repeat("BROWSER EDITOR Ω ", 4096)
	start := make(chan struct{})
	results := make(chan Matcher, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			pid, active := int32(741), true
			results <- CompileMatch(Match{TitleContains: title, PID: &pid, Active: &active})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for matcher := range results {
		if matcher.PID == nil || matcher.Active == nil || *matcher.PID != 741 || !*matcher.Active || matcher.titleContainsLower != strings.ToLower(title) {
			t.Fatal("contended cache returned a partially initialized matcher")
		}
		*matcher.PID = -1
		*matcher.Active = false
	}
	matched := CompileMatch(Match{TitleContains: title, PID: ptrInt32(741), Active: ptrBool(true)})
	if matched.PID == nil || matched.Active == nil || *matched.PID != 741 || !*matched.Active {
		t.Fatal("one concurrent caller contaminated the shared cache")
	}
	compiledMatchCacheMu.RLock()
	n := len(compiledMatchCache)
	compiledMatchCacheMu.RUnlock()
	if n != 1 {
		t.Fatalf("cache published %d entries for one key", n)
	}
}

// Concurrent cold-cache callers must see independently owned predicate pointers,
// even when another caller publishes the same compiled key first.
func TestCompileMatchConcurrentColdCacheIsolation(t *testing.T) {
	compiledMatchCacheMu.Lock()
	previous := compiledMatchCache
	compiledMatchCache = make(map[matchCacheKey]Matcher)
	compiledMatchCacheMu.Unlock()
	t.Cleanup(func() {
		compiledMatchCacheMu.Lock()
		compiledMatchCache = previous
		compiledMatchCacheMu.Unlock()
	})

	const callers = 128
	start := make(chan struct{})
	results := make(chan Matcher, callers)
	var workers sync.WaitGroup
	for i := 0; i < callers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			pid, active := int32(37), true
			results <- CompileMatch(Match{TitleContains: "BROWSER", PID: &pid, Active: &active})
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	for got := range results {
		if got.PID == nil || got.Active == nil || *got.PID != 37 || !*got.Active {
			t.Fatalf("concurrent compiled matcher lost its predicates: %+v", got)
		}
		*got.PID = -1
		*got.Active = false
	}
	pid, active := int32(37), true
	fresh := CompileMatch(Match{TitleContains: "BROWSER", PID: &pid, Active: &active})
	if fresh.PID == nil || fresh.Active == nil || *fresh.PID != 37 || !*fresh.Active {
		t.Fatalf("compiled cache leaked caller mutations: %+v", fresh)
	}
	compiledMatchCacheMu.RLock()
	entries := len(compiledMatchCache)
	compiledMatchCacheMu.RUnlock()
	if entries != 1 {
		t.Fatalf("concurrent identical keys created %d cache entries, want one", entries)
	}
}

// A matcher cache key must distinguish both the presence and value of every
// optional window predicate, even when a prior caller mutates its input and
// the returned matcher. In particular, nil and explicit false are different.
func TestCompileMatchAllOptionalPredicateCacheIdentity(t *testing.T) {
	isolatedCompiledMatchCache(t)
	pid, id := int32(418), uint64(72)
	active, minimized, maximized, fullscreen := true, false, true, false
	request := Match{
		TitleContains: "editor", PID: &pid, ID: &id,
		Active: &active, Minimized: &minimized,
		Maximized: &maximized, Fullscreen: &fullscreen,
	}
	original := Info{Title: "Editor", PID: pid, ID: id, Active: active,
		Minimized: minimized, Maximized: maximized, Fullscreen: fullscreen}
	compiled := CompileMatch(request)
	if !compiled.Matches(original) {
		t.Fatal("full predicate matcher rejected its original window")
	}
	pid, id = 999, 999
	active, minimized, maximized, fullscreen = false, true, false, true
	if !compiled.Matches(original) {
		t.Fatal("source predicate mutation changed compiled matcher")
	}
	*compiled.PID, *compiled.ID = 999, 999
	*compiled.Active, *compiled.Minimized = false, true
	*compiled.Maximized, *compiled.Fullscreen = false, true
	fresh := CompileMatch(Match{TitleContains: "editor", PID: ptrInt32(418), ID: ptrUint64(72),
		Active: ptrBool(true), Minimized: ptrBool(false), Maximized: ptrBool(true), Fullscreen: ptrBool(false)})
	if !fresh.Matches(original) {
		t.Fatal("mutating returned pointers polluted the shared cache")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Match)
	}{
		{"nil pid", func(m *Match) { m.PID = nil }},
		{"nil id", func(m *Match) { m.ID = nil }},
		{"nil active", func(m *Match) { m.Active = nil }},
		{"nil minimized", func(m *Match) { m.Minimized = nil }},
		{"nil maximized", func(m *Match) { m.Maximized = nil }},
		{"nil fullscreen", func(m *Match) { m.Fullscreen = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Match{TitleContains: "editor", PID: ptrInt32(418), ID: ptrUint64(72),
				Active: ptrBool(true), Minimized: ptrBool(false), Maximized: ptrBool(true), Fullscreen: ptrBool(false)}
			tc.mutate(&m)
			got := CompileMatch(m)
			if !got.Matches(original) {
				t.Fatalf("omitting predicate %s unexpectedly rejects original", tc.name)
			}
			if matchCacheKeyFromMatch(m) == matchCacheKeyFromMatch(fresh.Match) {
				t.Fatalf("omitting predicate %s collided with fully specified cache key", tc.name)
			}
		})
	}
}

func TestCompileMatchCacheIsBounded(t *testing.T) {
	const limit = 1024
	for i := 0; i < limit*2; i++ {
		CompileMatch(Match{TitleContains: fmt.Sprintf("audit-unique-%d", i)})
	}

	compiledMatchCacheMu.RLock()
	entries := len(compiledMatchCache)
	compiledMatchCacheMu.RUnlock()
	if entries > limit {
		t.Fatalf("compiled match cache entries = %d, want <= %d", entries, limit)
	}
}

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

// TestContainsFoldLowerMatchesLowerLowerContains proves that the
// allocation-free fold matches the previous strings.Contains(strings.ToLower,
// strings.ToLower) semantics exactly, including runes whose lowercase form has
// a different byte length.
func TestContainsFoldLowerMatchesLowerLowerContains(t *testing.T) {
	corpus := []struct{ s, sub string }{
		{"Firefox Web Browser", "firefox"},
		{"Firefox Web Browser", "WEB"},
		{"Firefox Web Browser", "browser"},
		{"Firefox Web Browser", ""},
		{"", ""},
		{"", "x"},
		{"é", "ée"},
		{"a", "abc"},
		{"abc", "abcd"},
		{"CAFÉ", "café"},
		{"café", "CAFÉ"},
		{"café au lait", "é au"},
		{"STRASSE", "straße"},
		{"straße", "STRASSE"},
		{"İstanbul", "istanbul"},
		{"Istanbul", "İstanbul"},
		{"ı", "I"},
		{"I", "ı"},
		{"ẞETA", "ßeta"},
		{"eßential", "ESSENTIAL"},
		{"aİb", "ai"},
		{"aİb", "aİ"},
		{"A" + "İ" + "B", "aib"},
		{"no match here", "zzz"},
		{"abc", "b"},
		{"abc", "bc"},
		{"abc", "ab"},
		{"AbC", "abc"},
		{"x1y2", "1Y"},
		{"mixed CASE string", "CASE"},
		{"ÉÉ", "éé"},
		{"ÉÉÉ", "é"},
		{"aeiou", "iou"},
		{"привет", "ПРИВЕТ"},
		{"ПРИВЕТ", "рив"},
		{"déjà vu", "DÉJÀ"},
		{"Resumé", "resume"},
		{"Resumé", "é"},
		{"Ωmega", "omega"},
		{"omega", "Ω"},
	}
	for _, pair := range corpus {
		got := containsFoldLower(pair.s, strings.ToLower(pair.sub))
		want := strings.Contains(strings.ToLower(pair.s), strings.ToLower(pair.sub))
		if got != want {
			t.Errorf("containsFoldLower(%q, %q) = %v, want %v", pair.s, strings.ToLower(pair.sub), got, want)
		}
	}
}

func TestMatcherTitleContainsFoldEquivalent(t *testing.T) {
	infos := []Info{
		{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox", Active: true},
		{ID: 2, Title: "CAFÉ", AppID: "org.example"},
		{ID: 3, Title: "İstanbul Airport", AppID: "org.example"},
		{ID: 4, Title: "STRASSE", AppID: "org.example"},
		{ID: 5, Title: "Window 5", AppID: "org.example", Minimized: true},
		{ID: 6, Title: "é", AppID: "org.example"},
	}
	substrings := []string{"firefox", "café", "İstanbul", "strasse", "straße", "window", "ée", ""}
	for _, title := range infos {
		for _, sub := range substrings {
			previous := strings.Contains(strings.ToLower(title.Title), strings.ToLower(sub))
			match := Match{TitleContains: sub}
			if got := match.Matches(title); got != previous {
				t.Errorf("Match{TitleContains:%q}.Matches(%q) = %v, want %v", sub, title.Title, got, previous)
			}
			matcher := CompileMatch(Match{TitleContains: sub})
			if got := matcher.Matches(title); got != previous {
				t.Errorf("CompileMatch(Match{TitleContains:%q}).Matches(%q) = %v, want %v", sub, title.Title, got, previous)
			}
		}
	}
}

// Older match specifications treated unknown backslash sequences as an
// escaped literal character. These are not valid Go quoted string escapes,
// so tokenizer fallback must retain the legacy meaning without leaking a
// backslash or dropping the rest of the predicate.
func TestTokenizeMatchSpecLegacyUnknownQuotedEscapesRemainCompatible(t *testing.T) {
	for _, tc := range []struct {
		name, spec string
		want       []string
	}{
		{"single unknown escape", `title="folder\q"`, []string{"title=folderq"}},
		{"mixed escaped slash and invalid escape", `class='a\q\\b'`, []string{`class=aq\b`}},
		{"quoted path with unknown escape and separators", `title="first\q second" active`, []string{"title=firstq second", "active"}},
		{"repeated legacy escapes", `app="a\zb\yc"`, []string{"app=azbyc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tokenizeMatchSpec(tc.spec)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("legacy match tokenization %q: got=%q want=%q err=%v", tc.spec, got, tc.want, err)
			}
		})
	}
}

func TestMatchSpecificationLegacyEscapeKeepsPredicatesAndRejectsUnterminatedQuotes(t *testing.T) {
	m, err := ParseMatchSpec(`title="Firefox\q Browser" active`)
	if err != nil {
		t.Fatal(err)
	}
	if m.TitleExact != "Firefoxq Browser" || m.Active == nil || !*m.Active {
		t.Fatalf("legacy escape corrupted parsed predicates: %+v", m)
	}
	if _, err := ParseMatchSpec(`title="Firefox\q`); err == nil {
		t.Fatal("unterminated legacy quote accepted")
	}
}

// Match.String produces strconv-quoted values. ParseMatchSpec must decode
// that representation without losing control characters or backslashes.
func TestMatchStringParseRoundTripQuotedControlCharacters(t *testing.T) {
	cases := []string{"line one\nline two", "tabs\tinside", "carriage\rreturn", "back\\slash", "quoted \"title\"", "control\x01byte"}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			original := Match{TitleExact: value, TitleContains: value, AppID: value, Class: value}
			parsed, err := ParseMatchSpec(original.String())
			if err != nil {
				t.Fatalf("parse %q: %v", original.String(), err)
			}
			if parsed.TitleExact != value || parsed.TitleContains != value || parsed.AppID != value || parsed.Class != value {
				t.Fatalf("round trip %q: parsed %#v", original.String(), parsed)
			}
		})
	}
}

func int32Ptr(v int32) *int32    { return new(v) }
func uint64Ptr(v uint64) *uint64 { return new(v) }

func TestParseMatchSpec(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want Match
	}{
		{
			name: "title substring shorthand",
			spec: "editor",
			want: Match{TitleContains: "editor"},
		},
		{
			name: "structured match",
			spec: `title="Exact Title" app_id=org.example class=Example pid=42 id=7 state:active state:-minimized state:fullscreen visible:true`,
			want: Match{
				TitleExact:  "Exact Title",
				AppID:       "org.example",
				Class:       "Example",
				PID:         int32Ptr(42),
				ID:          uint64Ptr(7),
				Active:      boolPtr(true),
				Minimized:   boolPtr(false),
				Fullscreen:  boolPtr(true),
				VisibleOnly: true,
			},
		},
		{
			name: "substring key",
			spec: `title~=browser`,
			want: Match{TitleContains: "browser"},
		},
		{
			name: "visible false",
			spec: `visible:false`,
			want: Match{},
		},
		{
			name: "empty separators are ignored",
			spec: ",  active,, visible:true, ",
			want: Match{Active: boolPtr(true), VisibleOnly: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMatchSpec(tt.spec)
			if err != nil {
				t.Fatalf("ParseMatchSpec(%q) error = %v", tt.spec, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseMatchSpec(%q) = %+v, want %+v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestParseMatchSpec_Invalid(t *testing.T) {
	tests := []string{
		`pid=abc`,
		`id=abc`,
		`state:bogus`,
		`title="unterminated`,
	}
	for _, spec := range tests {
		t.Run(spec, func(t *testing.T) {
			if _, err := ParseMatchSpec(spec); err == nil {
				t.Fatalf("ParseMatchSpec(%q) expected error, got nil", spec)
			}
		})
	}
}

func TestMatch_Matches(t *testing.T) {
	info := Info{
		ID:         7,
		Title:      "Exact Title",
		AppID:      "org.example",
		Class:      "Example",
		PID:        42,
		Active:     true,
		Minimized:  false,
		Maximized:  true,
		Fullscreen: true,
	}

	tests := []struct {
		name  string
		match Match
		want  bool
	}{
		{name: "substring", match: Match{TitleContains: "exact"}, want: true},
		{name: "exact title", match: Match{TitleExact: "exact title"}, want: true},
		{name: "app id", match: Match{AppID: "ORG.EXAMPLE"}, want: true},
		{name: "class", match: Match{Class: "example"}, want: true},
		{name: "pid", match: Match{PID: int32Ptr(42)}, want: true},
		{name: "id", match: Match{ID: uint64Ptr(7)}, want: true},
		{name: "active", match: Match{Active: boolPtr(true)}, want: true},
		{name: "minimized false", match: Match{Minimized: boolPtr(false)}, want: true},
		{name: "maximized", match: Match{Maximized: boolPtr(true)}, want: true},
		{name: "fullscreen", match: Match{Fullscreen: boolPtr(true)}, want: true},
		{name: "visible only", match: Match{VisibleOnly: true}, want: true},
		{name: "state negation", match: Match{Minimized: boolPtr(false)}, want: true},
		{name: "wrong pid", match: Match{PID: int32Ptr(99)}, want: false},
		{name: "wrong class", match: Match{Class: "terminal"}, want: false},
	}

	minimized := Info{ID: 9, Title: "Hidden", Minimized: true}
	if (Match{VisibleOnly: true}).Matches(minimized) {
		t.Fatal("VisibleOnly should reject minimized windows")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.match.Matches(info); got != tt.want {
				t.Fatalf("Match(%+v).Matches() = %v, want %v", tt.match, got, tt.want)
			}
		})
	}
}

func TestApplyToplevelString(t *testing.T) {
	encode := encodeWaylandTestString

	info := &Info{}
	if !applyToplevelString(info, 0, encode("Title")) {
		t.Fatal("applyToplevelString(title) returned false")
	}
	if info.Title != "Title" {
		t.Fatalf("title = %q, want %q", info.Title, "Title")
	}
	if !applyToplevelString(info, 1, encode("org.example")) {
		t.Fatal("applyToplevelString(app_id) returned false")
	}
	if info.AppID != "org.example" {
		t.Fatalf("app id = %q, want %q", info.AppID, "org.example")
	}
	if applyToplevelString(info, 99, encode("ignored")) {
		t.Fatal("applyToplevelString for unknown opcode returned true")
	}
	oversized := []byte{0xff, 0xff, 0xff, 0xff}
	if applyToplevelString(info, 0, oversized) {
		t.Fatal("applyToplevelString accepted an oversized length")
	}
	malformed := encodeWaylandTestString("Title")
	malformed[len(malformed)-1] = 'x'
	if applyToplevelString(info, 0, malformed) {
		t.Fatal("applyToplevelString accepted a string without a NUL terminator")
	}
}

func ExampleMatch_String() {
	m := Match{TitleContains: "editor", AppID: "org.example", Active: boolPtr(true)}
	fmt.Println(m.String())
	// Output:
	// title~="editor" app_id="org.example" active=true
}

func BenchmarkMatchMatches_TitleContains(b *testing.B) {
	info := Info{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox", Active: true}
	m := Match{TitleContains: "firefox"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.Matches(info)
	}
}

func BenchmarkMatcherMatches_TitleContains(b *testing.B) {
	info := Info{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox", Active: true}
	matcher := CompileMatch(Match{TitleContains: "firefox"})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = matcher.Matches(info)
	}
}

func BenchmarkMatchMatches_TitleExact(b *testing.B) {
	info := Info{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox"}
	m := Match{TitleExact: "firefox web browser"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.Matches(info)
	}
}

func BenchmarkMatchMatches_MultiField(b *testing.B) {
	active := true
	info := Info{ID: 7, Title: "Editor", AppID: "org.example.editor", PID: 42, Active: true}
	m := Match{TitleContains: "editor", AppID: "org.example.editor", Active: &active}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.Matches(info)
	}
}

func BenchmarkFindByTitle(b *testing.B) {
	wins := make([]Info, 20)
	for i := range wins {
		wins[i] = Info{ID: uint64(i + 1), Title: "Window " + string(rune('A'+i)), AppID: "org.example.app"}
	}
	wins[19].Title = "Target Window"
	m := &fakeManager{wins: wins}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = FindByTitle(context.Background(), m, "target")
	}
}

func BenchmarkFind_MatchByAppID(b *testing.B) {
	wins := make([]Info, 20)
	for i := range wins {
		wins[i] = Info{ID: uint64(i + 1), Title: "Window", AppID: "org.other.app"}
	}
	wins[19].AppID = "org.target.app"
	m := &fakeManager{wins: wins}
	match := Match{AppID: "org.target.app"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = FindByTitle(context.Background(), m, match.TitleContains)
	}
}

// errIterManager is a Manager whose IterateWindows yields a single error,
// simulating an I/O failure from the underlying window-list backend.
type errIterManager struct {
	err error
}

func (e *errIterManager) List(_ context.Context) ([]Info, error) { return nil, e.err }
func (e *errIterManager) IterateWindows(_ context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		yield(Info{}, e.err)
	}
}
func (e *errIterManager) Activate(_ context.Context, _ string) error         { return nil }
func (e *errIterManager) Move(_ context.Context, _ string, _, _ int) error   { return nil }
func (e *errIterManager) Resize(_ context.Context, _ string, _, _ int) error { return nil }
func (e *errIterManager) ActiveTitle(_ context.Context) (string, error)      { return "", nil }
func (e *errIterManager) CloseWindow(_ context.Context, _ string) error      { return nil }
func (e *errIterManager) Minimize(_ context.Context, _ string) error         { return nil }
func (e *errIterManager) Maximize(_ context.Context, _ string) error         { return nil }
func (e *errIterManager) Fullscreen(_ context.Context, _ string) error       { return nil }
func (e *errIterManager) Unfullscreen(_ context.Context, _ string) error     { return nil }
func (e *errIterManager) Restore(_ context.Context, _ string) error          { return nil }
func (e *errIterManager) Close() error                                       { return nil }

// TestWaitForMatchClose_PropagatesIOError verifies that WaitForMatchClose does
// not return nil when Find returns an error that is NOT ErrWindowNotFound.
//
// Prior to the fix, WaitForMatchClose returned nil for any error from Find,
// silently swallowing I/O errors and context-cancellation errors and
// incorrectly signalling that the window had closed.
func TestWaitForMatchClose_PropagatesIOError(t *testing.T) {
	ioErr := fmt.Errorf("backend: connection lost")
	m := &errIterManager{err: ioErr}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := WaitForMatchClose(ctx, m, Match{TitleContains: "anything"}, 10*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForMatchClose: got nil but expected the underlying I/O error to be propagated")
	}
	if !errors.Is(err, ioErr) {
		t.Fatalf("WaitForMatchClose: got %v, want error wrapping %v", err, ioErr)
	}
}

// TestWaitForMatchClose_SucceedsOnErrWindowNotFound verifies that the
// ErrWindowNotFound path still works correctly after the fix: the function
// must return nil when the window genuinely disappears.
func TestWaitForMatchClose_SucceedsOnErrWindowNotFound(t *testing.T) {
	// No windows → Find returns ErrWindowNotFound immediately.
	m := &fakeManager{wins: nil}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := WaitForMatchClose(ctx, m, Match{TitleContains: "anything"}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForMatchClose: got %v, want nil when window is not found", err)
	}
}

type cancellingIteratorManager struct {
	*fakeManager
	cancel  context.CancelFunc
	windows []Info
}

func (m *cancellingIteratorManager) IterateWindows(context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		m.cancel()
		for _, win := range m.windows {
			if !yield(win, nil) {
				return
			}
		}
	}
}
func TestFindByTitleDoesNotAcceptMatchAfterIteratorCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &cancellingIteratorManager{fakeManager: &fakeManager{}, cancel: cancel, windows: []Info{{ID: 1, Title: "Firefox"}}}
	_, err := FindByTitle(ctx, mgr, "Firefox")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled iterator found window: %v", err)
	}
}
func TestFindByTitleDoesNotReportNotFoundAfterIteratorCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &cancellingIteratorManager{fakeManager: &fakeManager{}, cancel: cancel}
	_, err := FindByTitle(ctx, mgr, "Firefox")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled iterator reported notfound: %v", err)
	}
}

// ── clampPoll ─────────────────────────────────────────────────────────────────

func TestClampPoll_Zero(t *testing.T) {
	got := clampPoll(0)
	if got != 10*time.Millisecond {
		t.Fatalf("clampPoll(0) = %v, want 10ms", got)
	}
}

func TestClampPoll_Negative(t *testing.T) {
	got := clampPoll(-5 * time.Millisecond)
	if got != 10*time.Millisecond {
		t.Fatalf("clampPoll(-5ms) = %v, want 10ms", got)
	}
}

func TestClampPoll_Positive(t *testing.T) {
	got := clampPoll(20 * time.Millisecond)
	if got != 20*time.Millisecond {
		t.Fatalf("clampPoll(20ms) = %v, want 20ms", got)
	}
}

// Window lookups must not return stale matches when their owner has already
// canceled the request, even if a backend is otherwise available.
func TestFindByIDPreCanceledRequestDoesNotEnumerateWindows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := FindByID(ctx, &fakeManager{wins: []Info{{ID: 42}}}, 42)
	if !errors.Is(err, context.Canceled) || got.ID != 0 {
		t.Fatalf("canceled FindByID = (%+v, %v), want no window and canceled", got, err)
	}
}

// A stream I/O error is not equivalent to an empty window listing.
func TestFindByTitleReturnsBackendIterationError(t *testing.T) {
	backendErr := errors.New("window enumeration transport disconnected")
	got, err := FindByTitle(context.Background(), &errIterManager{err: backendErr}, "editor")
	if !errors.Is(err, backendErr) || got.ID != 0 {
		t.Fatalf("failing FindByTitle = (%+v, %v), want backend error", got, err)
	}
}

// A missing backend and an already-canceled wait are distinct terminal cases:
// neither is success just because the window cannot be observed.
func TestWaitForMatchCloseMissingBackendAndCanceledWait(t *testing.T) {
	if err := WaitForMatchClose(context.Background(), nil, Match{TitleContains: "editor"}, time.Millisecond); err == nil {
		t.Fatal("nil backend incorrectly reported window closed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitForMatchClose(ctx, &fakeManager{}, Match{TitleContains: "editor"}, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait reported window closed: %v", err)
	}
}

// Cancellation occurring inside an empty iterator must take precedence over
// a successful close observation, even when no window was yielded.
func TestWaitForMatchCloseRejectsCancellationAfterEmptyIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &cancellingIteratorManager{fakeManager: &fakeManager{}, cancel: cancel}
	err := WaitForMatchClose(ctx, manager, Match{TitleContains: "editor"}, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during enumeration was treated as close: %v", err)
	}
}

// fakeManager implements Manager for testing FindByTitle.
type fakeManager struct {
	wins []Info
}

func (f *fakeManager) List(ctx context.Context) ([]Info, error) {
	return f.wins, nil
}

func (f *fakeManager) IterateWindows(ctx context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		for _, w := range f.wins {
			if !yield(w, nil) {
				return
			}
		}
	}
}

func (f *fakeManager) Activate(ctx context.Context, _ string) error         { return nil }
func (f *fakeManager) Move(ctx context.Context, _ string, _, _ int) error   { return nil }
func (f *fakeManager) Resize(ctx context.Context, _ string, _, _ int) error { return nil }
func (f *fakeManager) ActiveTitle(ctx context.Context) (string, error)      { return "", nil }
func (f *fakeManager) CloseWindow(ctx context.Context, _ string) error      { return nil }
func (f *fakeManager) Minimize(ctx context.Context, _ string) error         { return nil }
func (f *fakeManager) Maximize(ctx context.Context, _ string) error         { return nil }
func (f *fakeManager) Fullscreen(ctx context.Context, _ string) error       { return nil }
func (f *fakeManager) Unfullscreen(ctx context.Context, _ string) error     { return nil }
func (f *fakeManager) Restore(ctx context.Context, _ string) error          { return nil }
func (f *fakeManager) Close() error                                         { return nil }
func TestFindByTitle_FindsMatch(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "Hello World"}, {ID: 2, Title: "Other"}}}
	w, err := FindByTitle(context.Background(), m, "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.ID != 1 {
		t.Fatalf("expected ID 1, got %d", w.ID)
	}
}

func TestFindByTitle_NotFound(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "Foo"}}}
	_, err := FindByTitle(context.Background(), m, "bar")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "window: not found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestFindByTitleNilManager(t *testing.T) {
	var m Manager = (*fakeManager)(nil)

	_, err := FindByTitle(context.Background(), m, "hello")
	if err == nil {
		t.Fatal("expected error for nil manager")
	}
}

func TestPlan6ExplicitEmptyTitleIsNotWildcard(t *testing.T) {
	for _, spec := range []string{`title=""`, `title~=""`, `title="  "`, `title=`, `title~:`} {
		if _, err := ParseMatchSpec(spec); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%q: %v", spec, err)
		}
	}
	if m, err := ParseMatchSpec(""); err != nil || !m.Matches(Info{Title: "Editor"}) {
		t.Fatalf("unfiltered: %v %v", m, err)
	}
}

func TestPlan6InvalidTitleCannotProveLowLevelAbsence(t *testing.T) {
	manager := &fakeManager{wins: []Info{{Title: "Editor"}}}
	if err := WaitForMatchClose(context.Background(), manager, Title(""), time.Millisecond); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid selector proved absence: %v", err)
	}
	if _, err := FindByTitle(context.Background(), manager, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty substring selected a window: %v", err)
	}
}
