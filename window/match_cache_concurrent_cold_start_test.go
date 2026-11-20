package window

import (
	"sync"
	"testing"
)

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
