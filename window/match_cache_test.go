package window

import (
	"fmt"
	"testing"
)

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
