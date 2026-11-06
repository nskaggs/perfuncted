package perfuncted

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Retention scans a caller-supplied parent. Expired private session folders
// may be removed, but unrelated files, symlinks, and young sessions must not
// be removed even when their names resemble managed session directories.
func TestSessionLogRetentionPreservesForeignEntriesAndRecentSessions(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	old := filepath.Join(root, sessionLogPrefix+"expired")
	recent := filepath.Join(root, sessionLogPrefix+"recent")
	foreign := filepath.Join(root, "caller-owned")
	externalData := filepath.Join(external, "important")
	for _, directory := range []string{old, recent, foreign} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(externalData, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, sessionLogPrefix+"external-link")
	if err := os.Symlink(external, alias); err != nil {
		t.Fatal(err)
	}
	matchingFile := filepath.Join(root, sessionLogPrefix+"ordinary-file")
	if err := os.WriteFile(matchingFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-72 * time.Hour)
	for _, path := range []string{old, foreign, matchingFile} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	CleanupSessionLogs(root, 24*time.Hour)
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired managed session remains: %v", err)
	}
	for _, path := range []string{recent, foreign, matchingFile, alias, externalData} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("retention touched unrelated or recent entry %q: %v", path, err)
		}
	}
	if bytes, err := os.ReadFile(externalData); err != nil || string(bytes) != "untouched" {
		t.Fatalf("symlink target modified: %q %v", bytes, err)
	}
}

func TestSessionLogRetentionDisabledOrMissingRootNeverRemovesEntries(t *testing.T) {
	root := t.TempDir()
	expired := filepath.Join(root, sessionLogPrefix+"old")
	if err := os.Mkdir(expired, 0700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(expired, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		root string
		age  time.Duration
	}{{root, 0}, {root, -time.Second}, {"", time.Second}, {filepath.Join(root, "nonexistent"), time.Second}} {
		CleanupSessionLogs(tc.root, tc.age)
		if _, err := os.Stat(expired); err != nil {
			t.Fatalf("disabled/missing root removed existing directory: %v", err)
		}
	}
}

func TestSessionLogRootRejectsSymlinksAndRegularFiles(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "regular")
	if err := os.WriteFile(file, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{alias, file} {
		if dir, err := createSessionLogDir(path); err == nil {
			_ = os.RemoveAll(dir)
			t.Fatalf("unsafe log root accepted: %q", path)
		}
	}
	if entries, err := os.ReadDir(real); err != nil || len(entries) != 0 {
		t.Fatalf("symlink root altered target: %v %v", entries, err)
	}
}
