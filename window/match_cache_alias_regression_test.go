package window

import "testing"

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
