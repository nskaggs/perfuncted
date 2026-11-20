package find

import "testing"

// A definite external change invalidates earlier matching hash samples.
// A subsequent matching hash must rebuild the required stable streak.
func TestStableStreakConfirmedChangeResetsEarlierSamples(t *testing.T) {
	const hash uint32 = 0x12345678
	streak := newStableStreak(hash, 3)
	if done, _ := streak.advance(hash); done {
		t.Fatal("two matching samples declared three-sample stability")
	}
	streak.changed()
	if streak.count != 0 {
		t.Fatalf("confirmed change retained %d previous samples", streak.count)
	}
	for i := 1; i < 3; i++ {
		if done, got := streak.advance(hash); done || got != hash {
			t.Fatalf("post-change sample %d: done=%v hash=%x", i, done, got)
		}
	}
	if done, got := streak.advance(hash); !done || got != hash {
		t.Fatalf("third post-change hash did not establish stability: done=%v hash=%x", done, got)
	}
}
