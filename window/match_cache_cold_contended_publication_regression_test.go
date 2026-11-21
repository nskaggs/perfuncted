package window

import (
	"strings"
	"sync"
	"testing"
)

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
