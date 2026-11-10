//go:build integration && linux

package flatpaktest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// writeFlatpakBuildInfo stamps the packaged executable from the exact Git
// revision being packaged, not from a potentially stale AppStream version.
// A local manifest build without this optional file still produces a dev CLI.
func writeFlatpakBuildInfo(sourceRoot string) error {
	runGit := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", sourceRoot}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %v: %w: %s", args, err, out)
		}
		return strings.TrimSpace(string(out)), nil
	}
	commit, err := runGit("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if !regexp.MustCompile("^[a-f0-9]{40}$").MatchString(commit) {
		return fmt.Errorf("untrusted Flatpak commit identity %q", commit)
	}
	date, err := runGit("show", "-s", "--date=iso-strict", "--format=%cd", "HEAD")
	if err != nil {
		return err
	}
	if !regexp.MustCompile("^[0-9TZ:+-]+$").MatchString(date) {
		return fmt.Errorf("untrusted Flatpak commit date %q", date)
	}
	version := "dev-" + commit[:12]
	if tag, tagErr := runGit("describe", "--tags", "--exact-match", "HEAD"); tagErr == nil {
		if !regexp.MustCompile("^v[0-9]+[.][0-9]+[.][0-9]+$").MatchString(tag) {
			return fmt.Errorf("unsupported Flatpak release tag %q", tag)
		}
		version = tag
	}
	path := filepath.Join(sourceRoot, "flatpak", "build-info.txt")
	return os.WriteFile(path, []byte(version+"\n"+commit+"\n"+date+"\n"), 0o644)
}
