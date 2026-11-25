package runtime

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// An attacker who can preplace a hard-linked PID entry must not make an
// unrelated inode authoritative evidence for managed-process cleanup.
// Metadata may be valid decimal text and mode 0600 while still being shared.
func TestSessionPIDMetadataRejectsForeignHardlinkOwnership(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "unrelated-private-record")
	const privatePID = "74231\n"
	if err := os.WriteFile(foreign, []byte(privatePID), 0600); err != nil {
		t.Fatal(err)
	}
	linkedPID := filepath.Join(dir, "dbus.pid")
	if err := os.Link(foreign, linkedPID); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadSessionPIDMetadata(linkedPID); err == nil {
		t.Fatalf("hard-linked ownership evidence accepted: %q", data)
	}
	if pid, err := ReadPIDFile(linkedPID); err == nil || pid != 0 {
		t.Fatalf("hard-linked PID admitted: %d %v", pid, err)
	}
	saved, err := os.ReadFile(foreign)
	if err != nil || string(saved) != privatePID {
		t.Fatalf("foreign file changed: %q %v", saved, err)
	}
	if err := os.Remove(linkedPID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkedPID, []byte(" 74231 \n"), 0600); err != nil {
		t.Fatal(err)
	}
	pid, err := ReadPIDFile(linkedPID)
	if err != nil || pid != 74231 {
		t.Fatalf("isolated private PID rejected: %d %v", pid, err)
	}
}

// Cleanup PID markers authorize process control. A file writable by another
// account cannot prove that its recorded PID belongs to this session, even
// when it is a regular file with one link and valid numeric contents.
func TestSessionCleanupRejectsOtherWritablePIDMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbus.pid")
	for _, tc := range []struct {
		name   string
		mode   os.FileMode
		accept bool
	}{
		{"owner-private", 0600, true},
		{"owner-readable", 0644, true},
		{"group-writable", 0620, false},
		{"world-writable", 0602, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte("12345\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			pid, err := ReadPIDFile(path)
			if tc.accept && (err != nil || pid != 12345) {
				t.Fatalf("valid PID rejected: %d %v", pid, err)
			}
			if !tc.accept && (err == nil || pid != 0) {
				t.Fatalf("untrusted writable PID accepted: %d %v", pid, err)
			}
		})
	}
}

// The atomic managed-file publisher must never expose a partial replacement
// or leave private staging files when publication fails. A directory at the
// destination forces rename failure after the temporary file has been written.
func TestManagedSessionFileAtomicPublishFailureLeavesNoStagedData(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "runtime.pid")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve unrelated state"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte("new private process metadata\n"), 4096)
	if err := WriteManagedSessionFile(target, secret, 0600); err == nil {
		t.Fatal("published file over existing directory")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "preserve unrelated state" {
		t.Fatalf("failed publication modified target directory: %q %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "runtime.pid" || !entries[0].IsDir() {
		t.Fatalf("failed publication left staged sensitive data: %v %v", entries, err)
	}
	// After the obstructing directory is removed, the same path must remain
	// usable for a correctly private, complete replacement.
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if err := WriteManagedSessionFile(target, secret, 0600); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(saved, secret) {
		t.Fatalf("successful replacement incomplete: bytes=%d err=%v", len(saved), err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("published metadata not private: %v %v", info, err)
	}
	entries, err = os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "runtime.pid" {
		t.Fatalf("successful publication left temporary files: %v %v", entries, err)
	}
}

func TestManagedSessionFileMissingParentDoesNotCreatePartialDestination(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "missing", "child.pid")
	if err := WriteManagedSessionFile(absent, []byte("private"), 0600); err == nil {
		t.Fatal("writing into absent parent succeeded")
	}
	if _, err := os.Stat(filepath.Dir(absent)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed write created unexpected directory: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed write leaked files: %v %v", entries, err)
	}
}

// An empty managed-runtime path must never become a wildcard over all host
// mounts. Cleanup is allowed to inspect only an explicitly scoped runtime.
func TestSessionMountInfoEmptyScopeNeverSelectsHostMounts(t *testing.T) {
	mountinfo := []byte("1 0 0:1 / / rw - tmpfs tmpfs rw\n2 1 0:2 / /var/tmp/private rw - tmpfs tmpfs rw\n")
	if matches := ParseSessionSubdirMounts("", mountinfo); len(matches) != 0 {
		t.Fatalf("empty runtime path selected host mounts: %v", matches)
	}
	if matches := ParseSessionSubdirMounts("/var/tmp/private-other", mountinfo); len(matches) != 0 {
		t.Fatalf("sibling runtime path selected foreign mounts: %v", matches)
	}
}

// The kernel escapes control characters and backslashes in mountinfo paths.
// Those encodings must be decoded before deciding whether the mount belongs to
// an owned runtime; names sharing only the prefix must never match.
func TestSessionMountinfoDecodesEveryKernelPathEscapeAndRejectsForeignPrefix(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-special"
	info := []byte(
		"1 0 0:1 / " + root + "/plain rw - tmpfs tmpfs rw\n" +
			"2 0 0:2 / " + root + "/tab\\011name rw - tmpfs tmpfs rw\n" +
			"3 0 0:3 / " + root + "/new\\012line rw - tmpfs tmpfs rw\n" +
			"4 0 0:4 / " + root + "/back\\134slash rw - tmpfs tmpfs rw\n" +
			"5 0 0:5 / " + root + "-foreign rw - tmpfs tmpfs rw\n" +
			"6 0 0:6 / " + root + "\\040foreign rw - tmpfs tmpfs rw\n" +
			"invalid\n")
	got := ParseSessionSubdirMounts(root, info)
	want := []string{root + "/plain", root + "/tab\tname", root + "/new\nline", root + "/back\\slash"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mount parsing crossed runtime boundary: got=%q want=%q", got, want)
	}
}
