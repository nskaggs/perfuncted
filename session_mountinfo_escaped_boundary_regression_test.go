package perfuncted

import (
	"reflect"
	"testing"
)

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
	got := parseSessionSubdirMounts(root, info)
	want := []string{root + "/plain", root + "/tab\tname", root + "/new\nline", root + "/back\\slash"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mount parsing crossed runtime boundary: got=%q want=%q", got, want)
	}
}
