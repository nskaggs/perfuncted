package window

import "testing"

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
