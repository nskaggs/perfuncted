package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Ownership metadata with a zero or negative process ID is malformed, not
// proof that the owner died. A newly created runtime must retain the same
// creation grace as an unreadable or nonnumeric owner file.
func TestManagedRuntimeNonpositiveOwnerPIDPreservesCreationGrace(t *testing.T) {
	for _, owner := range []string{"0", "-1", " -42 ", "not-a-pid"} {
		t.Run(owner, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "perfuncted-xdg-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			sentinel := filepath.Join(dir, "private-session-data")
			if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, sessionOwnerPIDFile), []byte(owner), 0600); err != nil {
				t.Fatal(err)
			}
			if err := reapSessionDirIfStale(context.Background(), dir, 0, time.Now()); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
				t.Fatalf("recent managed runtime with owner %q was removed: data=%q error=%v", owner, data, err)
			}
		})
	}
}

func TestMalformedManagedRuntimeOwnerHonorsMinimumAndConfiguredAge(t *testing.T) {
	for _, tc := range []struct {
		name, owner string
		age, maxAge time.Duration
		wantRemoved bool
	}{
		{"zero age below minimum", "0", 4 * time.Minute, 0, false},
		{"negative age below minimum", "-1", 4 * time.Minute, 0, false},
		{"zero age above minimum", "0", 6 * time.Minute, 0, true},
		{"negative age above minimum", "-1", 6 * time.Minute, 0, true},
		{"configured age protects zero", "0", 6 * time.Minute, time.Hour, false},
		{"configured age protects negative", "-5", 6 * time.Minute, time.Hour, false},
		{"configured age reaps zero", "0", 2 * time.Hour, time.Hour, true},
		{"configured age reaps negative", "-1", 2 * time.Hour, time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "perfuncted-xdg-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			sentinel := filepath.Join(dir, "session-sentinel")
			if err := os.WriteFile(sentinel, []byte("private"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, sessionOwnerPIDFile), []byte(tc.owner), 0600); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			old := now.Add(-tc.age)
			if err := os.Chtimes(dir, old, old); err != nil {
				t.Fatal(err)
			}
			if err := reapSessionDirIfStale(context.Background(), dir, tc.maxAge, now); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(sentinel)
			if tc.wantRemoved {
				if !os.IsNotExist(err) {
					t.Fatalf("expired malformed-owner runtime remains: %v %q", err, b)
				}
			} else if err != nil || string(b) != "private" {
				t.Fatalf("unexpired malformed-owner runtime removed: %v %q", err, b)
			}
		})
	}
}
