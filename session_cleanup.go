package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	cleanupStaleSessionsMu sync.Mutex
	lastCleanupTime        time.Time
	cleanupAttempt         uint64
)

const (
	sessionOwnerPIDFile     = "perfuncted.pid"
	noPIDFileReapGrace      = 5 * time.Minute
	cleanupStaleMinInterval = 30 * time.Second
)

var sessionChildPIDFiles = []string{
	"dbus.pid",
	"at-spi.pid",
	"sway.pid",
	"wl-paste.pid",
}

// CleanupStaleSessions removes perfuncted session directories immediately when
// their recorded parent PID is no longer running. Missing ownership metadata
// gets a five-minute creation grace; malformed metadata uses maxAge, clamped
// to that same minimum.
func CleanupStaleSessions(maxAge time.Duration) {
	_ = CleanupStaleSessionsContext(context.Background(), maxAge)
}

// CleanupStaleSessionsContext removes stale managed sessions while honoring
// cancellation during process shutdown and mount cleanup. Remaining directories
// are safe to retry on a later cleanup pass.
func CleanupStaleSessionsContext(ctx context.Context, maxAge time.Duration) error {
	if ctx == nil {
		return fmt.Errorf("session cleanup: %w: nil context", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return contextErrorWithCause(ctx)
	}
	// Claim the interval while locked, then scan outside it so concurrent Opens
	// don't wait for process termination. A canceled newest attempt releases its
	// claim without clearing a newer scan's rate limit.
	cleanupStaleSessionsMu.Lock()
	if time.Since(lastCleanupTime) < cleanupStaleMinInterval {
		cleanupStaleSessionsMu.Unlock()
		if err := ctx.Err(); err != nil {
			return contextErrorWithCause(ctx)
		}
		return nil
	}
	cleanupAttempt++
	attempt := cleanupAttempt
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()

	err := scanStaleSessions(ctx, maxAge)
	if err == nil && ctx.Err() != nil {
		err = contextErrorWithCause(ctx)
	}
	if err != nil {
		cleanupStaleSessionsMu.Lock()
		if cleanupAttempt == attempt {
			lastCleanupTime = time.Time{}
		}
		cleanupStaleSessionsMu.Unlock()
	}
	return err
}

func scanStaleSessions(ctx context.Context, maxAge time.Duration) error {
	matches, err := staleSessionDirectories(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, dir := range matches {
		if err := ctx.Err(); err != nil {
			return contextErrorWithCause(ctx)
		}
		if err := reapSessionDirIfStale(ctx, dir, maxAge, now); err != nil {
			return err
		}
	}
	return nil
}

func staleSessionDirectories(ctx context.Context) ([]string, error) {
	// Managed runtimes can fall back to a short /tmp path if caller TMPDIR
	// would make D-Bus sockets too long. Reap both locations with the existing
	// owner PID and strict managed-directory checks.
	var matches []string
	for _, root := range managedSessionTempRoots() {
		if err := ctx.Err(); err != nil {
			return nil, contextErrorWithCause(ctx)
		}
		found, err := filepath.Glob(filepath.Join(root, "perfuncted-xdg-*"))
		if err != nil {
			slog.Warn("unable to glob nested sessions", "error", err)
			continue
		}
		matches = append(matches, found...)
	}
	return matches, nil
}

func reapSessionDirIfStale(
	ctx context.Context,
	dir string,
	maxAge time.Duration,
	now time.Time,
) error {
	data, err := os.ReadFile(filepath.Join(dir, sessionOwnerPIDFile))
	if err != nil {
		return reapSessionDirWhenOlderThan(ctx, dir, now, noPIDFileReapGrace)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return reapSessionDirWhenOlderThan(ctx, dir, now, staleMalformedPIDThreshold(maxAge))
	}
	if !pidAlive(pid) {
		return reapSessionDir(ctx, dir)
	}
	return nil
}

func reapSessionDirWhenOlderThan(
	ctx context.Context,
	dir string,
	now time.Time,
	maxAge time.Duration,
) error {
	if err := ctx.Err(); err != nil {
		return contextErrorWithCause(ctx)
	}
	fi, err := os.Stat(dir)
	if err != nil || now.Sub(fi.ModTime()) <= maxAge {
		return nil
	}
	return reapSessionDir(ctx, dir)
}

func reapSessionDir(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return contextErrorWithCause(ctx)
	}
	if !isManagedSessionDir(dir) {
		slog.Debug("session: skip stale directory with unexpected path", "path", dir)
		return nil
	}
	for _, name := range sessionChildPIDFiles {
		if err := ctx.Err(); err != nil {
			return contextErrorWithCause(ctx)
		}
		pid, err := readPIDFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		// Stale-owner cleanup is an independent maintenance action. Its short
		// grace does not bound active Session process-stop operations.
		stopped, err := stopRecordedProcess(ctx, pid, dir, 100*time.Millisecond)
		if err != nil {
			return err
		}
		if !stopped {
			slog.Debug(
				"session: skip stale child with mismatched runtime directory",
				"pid", pid,
				"path", dir,
			)
			continue
		}
	}
	if err := unmountSubdirsContext(ctx, dir); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return contextErrorWithCause(ctx)
	}
	if err := os.RemoveAll(dir); err != nil {
		slog.Debug("session: reap remove dir", "path", dir, "error", err)
	}
	return nil
}

// unmountSubdirsContext must prove all mounts under a managed runtime have
// disappeared before callers may remove its directory tree. Otherwise RemoveAll
// could descend into a still-mounted filesystem and delete unrelated data.
func unmountSubdirsContext(ctx context.Context, dir string) error {
	return unmountSubdirsWith(ctx, dir, readSessionSubdirMounts, func(ctx context.Context, mount string) error {
		return exec.CommandContext(ctx, "fusermount", "-u", mount).Run()
	})
}

func unmountSubdirsWith(
	ctx context.Context,
	dir string,
	readMounts func(string) ([]string, error),
	unmount func(context.Context, string) error,
) error {
	if err := ctx.Err(); err != nil {
		return contextErrorWithCause(ctx)
	}
	if dir == "" {
		return nil
	}
	mounts, err := readMounts(dir)
	if err != nil {
		return fmt.Errorf("session: inspect mounts before cleanup: %w", err)
	}
	if len(mounts) == 0 {
		return nil
	}
	// Parent-first ordering from mountinfo is not an unmount contract. Always
	// retire nested mountpoints before their parents.
	sort.Slice(mounts, func(i, j int) bool { return len(mounts[i]) > len(mounts[j]) })
	for _, mount := range mounts {
		if err := ctx.Err(); err != nil {
			return contextErrorWithCause(ctx)
		}
		if err := unmount(ctx, mount); err != nil {
			if ctx.Err() != nil {
				return contextErrorWithCause(ctx)
			}
			slog.Debug("session: unmount helper failed", "error", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return contextErrorWithCause(ctx)
	}
	remaining, err := readMounts(dir)
	if err != nil {
		return fmt.Errorf("session: verify mounts after cleanup: %w", err)
	}
	if len(remaining) != 0 {
		return fmt.Errorf("session: refuse directory removal: %d mounts remain below managed runtime", len(remaining))
	}
	return nil
}

func readSessionSubdirMounts(dir string) ([]string, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseSessionSubdirMounts(dir, data), nil
}

// Linux mountinfo percent-encodes neither spaces nor backslashes: it encodes
// ASCII whitespace and '\\' as octal escapes. Compare decoded mountpoints
// rather than the raw fields, or a managed runtime with spaces can go unseen.
var mountInfoPathUnescape = strings.NewReplacer(
	`\040`, " ",
	`\011`, "\t",
	`\012`, "\n",
	`\134`, `\`,
)

func parseSessionSubdirMounts(dir string, data []byte) []string {
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	prefix := dir + string(filepath.Separator)
	var mounts []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mountpoint := mountInfoPathUnescape.Replace(fields[4])
		if mountpoint == dir || strings.HasPrefix(mountpoint, prefix) {
			mounts = append(mounts, mountpoint)
		}
	}
	return mounts
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid in %s", path)
	}
	return pid, nil
}

func processUsesRuntimeDir(pid int, dir string) bool {
	return processUsesRuntimeDirAt("/proc", pid, dir)
}

type processSnapshot struct {
	pid       int
	pgid      int
	startTime uint64
}

type processGroupOwner struct {
	pgid    int
	dir     string
	leader  processSnapshot
	members map[processSnapshot]struct{}
}

func newProcessGroupOwner(pid int, dir string) (*processGroupOwner, bool) {
	if pid <= 0 || dir == "" || !processUsesRuntimeDir(pid, dir) {
		return nil, false
	}
	leader, err := readProcessSnapshotAt("/proc", pid)
	if err != nil || leader.pgid != pid {
		return nil, false
	}
	return &processGroupOwner{
		pgid:    pid,
		dir:     dir,
		leader:  leader,
		members: make(map[processSnapshot]struct{}),
	}, true
}

func (o *processGroupOwner) refresh() bool {
	if o == nil || o.pgid <= 0 || o.dir == "" {
		return false
	}
	processes := processGroupMembersAt("/proc", o.pgid)
	leaderPresent := false
	leaderOwned := false
	ownedMember := false
	for _, process := range processes {
		if process.pid == o.leader.pid {
			if process.startTime != o.leader.startTime {
				return false
			}
			leaderPresent = true
			leaderOwned = processUsesRuntimeDirAt("/proc", process.pid, o.dir)
			continue
		}
		if _, tracked := o.members[process]; tracked &&
			processUsesRuntimeDirAt("/proc", process.pid, o.dir) {
			ownedMember = true
		}
	}
	if leaderPresent && leaderOwned {
		ownedMember = true
		for _, process := range processes {
			if process.pid == o.leader.pid {
				continue
			}
			if processUsesRuntimeDirAt("/proc", process.pid, o.dir) {
				o.members[process] = struct{}{}
			}
		}
	}
	return ownedMember
}

func processGroupMembersAt(procRoot string, pgid int) []processSnapshot {
	if procRoot == "" || pgid <= 0 {
		return nil
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	members := make([]processSnapshot, 0)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		process, err := readProcessSnapshotAt(procRoot, pid)
		if err == nil && process.pgid == pgid {
			members = append(members, process)
		}
	}
	return members
}

func readProcessSnapshotAt(procRoot string, pid int) (processSnapshot, error) {
	if procRoot == "" || pid <= 0 {
		return processSnapshot{}, fmt.Errorf("invalid process snapshot target")
	}
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return processSnapshot{}, err
	}
	stat := string(data)
	commEnd := strings.LastIndexByte(stat, ')')
	if commEnd < 0 || commEnd+2 >= len(stat) {
		return processSnapshot{}, fmt.Errorf("invalid process stat for %d", pid)
	}
	fields := strings.Fields(stat[commEnd+2:])
	if len(fields) <= 19 {
		return processSnapshot{}, fmt.Errorf("invalid process stat for %d", pid)
	}
	pgid, err := strconv.Atoi(fields[2])
	if err != nil || pgid <= 0 {
		return processSnapshot{}, fmt.Errorf("invalid process group for %d", pid)
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return processSnapshot{}, fmt.Errorf("invalid process start time for %d", pid)
	}
	return processSnapshot{pid: pid, pgid: pgid, startTime: startTime}, nil
}

func processUsesRuntimeDirAt(procRoot string, pid int, dir string) bool {
	if pid <= 0 || dir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "environ"))
	if err != nil {
		return false
	}
	want := "XDG_RUNTIME_DIR=" + dir
	for value := range strings.SplitSeq(string(data), "\x00") {
		if value == want {
			return true
		}
	}
	return false
}

func stopRecordedProcess(
	ctx context.Context,
	pid int,
	dir string,
	grace time.Duration,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, contextErrorWithCause(ctx)
	}
	owner, ok := newProcessGroupOwner(pid, dir)
	if !ok || !owner.refresh() {
		return false, nil
	}
	proc := &managedProc{pid: pid}
	if err := proc.signal(syscall.SIGTERM); err != nil {
		slog.Debug("session: terminate stale process group", "pid", pid, "error", err)
	}

	graceCtx, cancel := context.WithTimeout(ctx, grace)
	waitErr := waitForOwnedProcessGroup(graceCtx, owner)
	cancel()
	if waitErr == nil {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, contextErrorWithCause(ctx)
	}
	if !errors.Is(waitErr, context.DeadlineExceeded) {
		return false, waitErr
	}
	if !processGroupAlive(pid) || !owner.refresh() {
		return true, nil
	}
	if err := proc.signal(syscall.SIGKILL); err != nil {
		slog.Debug("session: kill stale process group", "pid", pid, "error", err)
	}
	postKillCtx, cancel := context.WithTimeout(ctx, grace)
	_ = waitForOwnedProcessGroup(postKillCtx, owner)
	cancel()
	if err := ctx.Err(); err != nil {
		return false, contextErrorWithCause(ctx)
	}
	return true, nil
}

func waitForOwnedProcessGroup(ctx context.Context, owner *processGroupOwner) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return contextErrorWithCause(ctx)
		}
		if !processGroupAlive(owner.pgid) || !owner.refresh() {
			return nil
		}
		select {
		case <-ctx.Done():
			return contextErrorWithCause(ctx)
		case <-ticker.C:
		}
	}
}

func staleMalformedPIDThreshold(maxAge time.Duration) time.Duration {
	if maxAge < noPIDFileReapGrace {
		return noPIDFileReapGrace
	}
	return maxAge
}
