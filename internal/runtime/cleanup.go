package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// OwnerPIDFile is the private runtime owner marker.
const OwnerPIDFile = "perfuncted.pid"
const noPIDFileReapGrace = 5 * time.Minute

var sessionChildPIDFiles = []string{
	"dbus.pid",
	"at-spi.pid",
	"sway.pid",
	"wl-paste.pid",
}

// ScanStaleSessions scans and reclaims only stale managed runtimes.
func ScanStaleSessions(ctx context.Context, maxAge time.Duration) error {
	matches, err := staleSessionDirectories(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, dir := range matches {
		if err := ctx.Err(); err != nil {
			return contextutil.ErrorWithCause(ctx)
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
	for _, root := range ManagedSessionTempRoots() {
		if err := ctx.Err(); err != nil {
			return nil, contextutil.ErrorWithCause(ctx)
		}
		// TMPDIR is an ordinary path, not a glob. Glob interprets brackets,
		// question marks and asterisks in the root, which can skip owned
		// sessions or scan unrelated directories. Only direct directories
		// qualify as managed runtimes; files and symlinks are not sessions.
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("session: inspect managed runtime root %q: %w", root, err)
		}
		for _, entry := range entries {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, contextutil.ErrorWithCause(ctx)
			}
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "perfuncted-xdg-") && len(entry.Name()) > len("perfuncted-xdg-") {
				matches = append(matches, filepath.Join(root, entry.Name()))
			}
		}
	}
	return matches, nil
}

func reapSessionDirIfStale(
	ctx context.Context,
	dir string,
	maxAge time.Duration,
	now time.Time,
) error {
	data, err := ReadSessionPIDMetadata(filepath.Join(dir, OwnerPIDFile))
	if err != nil {
		return reapSessionDirWhenOlderThan(ctx, dir, now, noPIDFileReapGrace)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		// A nonpositive PID is invalid ownership metadata, not proof that
		// an active newly-created session's owner has exited. Preserve the
		// minimum grace instead of immediately reaping its directory.
		return reapSessionDirWhenOlderThan(ctx, dir, now, staleMalformedPIDThreshold(maxAge))
	}
	if !PIDAlive(pid) {
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
		return contextutil.ErrorWithCause(ctx)
	}
	fi, err := os.Stat(dir)
	if err != nil || now.Sub(fi.ModTime()) <= maxAge {
		return nil
	}
	return reapSessionDir(ctx, dir)
}

func reapSessionDir(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return contextutil.ErrorWithCause(ctx)
	}
	if !IsManagedSessionDir(dir) {
		slog.Debug("session: skip stale directory with unexpected path", "path", dir)
		return nil
	}
	// Discovery is not ownership proof: a path can be replaced between
	// enumeration and reaping. Never remove a same-prefix file or symlink.
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("session: inspect stale runtime %q: %w", dir, err)
	}
	// A recognizable name cannot authorize deleting a replaced, shared
	// directory. Managed runtimes are created by our UID with mode 0700.
	if !IsPrivateSessionRuntimeInfo(info) {
		slog.Debug("session: skip stale runtime without private ownership", "path", dir)
		return nil
	}
	for _, name := range sessionChildPIDFiles {
		if err := ctx.Err(); err != nil {
			return contextutil.ErrorWithCause(ctx)
		}
		pid, err := ReadPIDFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		// Stale-owner cleanup is an independent maintenance action. Its short
		// grace does not bound active Session process-stop operations.
		stopped, err := StopRecordedProcess(ctx, pid, dir, 100*time.Millisecond)
		if err != nil {
			return err
		}
		if !stopped {
			if processUsesRuntimeDir(pid, dir) {
				// A live process may still depend on this directory even when its
				// process group is outside the cleaner's signaling authority.
				slog.Debug("session: preserve stale runtime used by unowned child", "pid", pid, "path", dir)
				return nil
			}
			slog.Debug(
				"session: skip stale child with mismatched runtime directory",
				"pid", pid,
				"path", dir,
			)
			continue
		}
	}
	if err := UnmountSubdirsContext(ctx, dir); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return contextutil.ErrorWithCause(ctx)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("session: remove stale runtime %q: %w", dir, err)
	}
	return nil
}

// IsPrivateSessionRuntimeInfo checks the inode resolved by Lstat; neither
// symlinks nor directories writable by other accounts authorize deletion.
func IsPrivateSessionRuntimeInfo(info os.FileInfo) bool {
	if info == nil || !info.IsDir() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0o022 == 0
}

// IsPrivateManagedSessionDir checks both the managed-name boundary and the
// current inode before allowing managed helpers to write session metadata.
func IsPrivateManagedSessionDir(dir string) bool {
	if !IsManagedSessionDir(dir) {
		return false
	}
	info, err := os.Lstat(dir)
	return err == nil && IsPrivateSessionRuntimeInfo(info)
}

// UnmountSubdirsContext must prove all mounts under a managed runtime have
// disappeared before callers may remove its directory tree. Otherwise RemoveAll
// could descend into a still-mounted filesystem and delete unrelated data.
func UnmountSubdirsContext(ctx context.Context, dir string) error {
	return unmountSubdirsWith(ctx, dir, ReadSessionSubdirMounts, func(ctx context.Context, mount string) error {
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
		return contextutil.ErrorWithCause(ctx)
	}
	if dir == "" {
		return nil
	}
	mounts, err := readMounts(dir)
	if ctx.Err() != nil {
		return errors.Join(contextutil.ErrorWithCause(ctx), err)
	}
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
		if ctx.Err() != nil {
			return contextutil.ErrorWithCause(ctx)
		}
		if unmountErr := unmount(ctx, mount); unmountErr != nil {
			if ctx.Err() != nil {
				return errors.Join(contextutil.ErrorWithCause(ctx), unmountErr)
			}
			slog.Debug("session: unmount helper failed", "error", unmountErr)
		}
	}
	if ctx.Err() != nil {
		return contextutil.ErrorWithCause(ctx)
	}
	remaining, err := readMounts(dir)
	if ctx.Err() != nil {
		return errors.Join(contextutil.ErrorWithCause(ctx), err)
	}
	if err != nil {
		return fmt.Errorf("session: verify mounts after cleanup: %w", err)
	}
	if len(remaining) != 0 {
		return fmt.Errorf("session: refuse directory removal: %d mounts remain below managed runtime", len(remaining))
	}
	return nil
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
	return o.refreshAt("/proc")
}

func (o *processGroupOwner) refreshAt(procRoot string) bool {
	if o == nil || o.pgid <= 0 || o.dir == "" {
		return false
	}
	processes := processGroupMembersAt(procRoot, o.pgid)
	leaderPresent := false
	leaderOwned := false
	ownedMember := false
	for _, process := range processes {
		if process.pid == o.leader.pid {
			if process.startTime != o.leader.startTime {
				return false
			}
			leaderPresent = true
			leaderOwned = processUsesRuntimeDirAt(procRoot, process.pid, o.dir)
			continue
		}
		if _, tracked := o.members[process]; tracked &&
			processUsesRuntimeDirAt(procRoot, process.pid, o.dir) {
			ownedMember = true
		}
	}
	if leaderPresent && leaderOwned {
		ownedMember = true
		for _, process := range processes {
			if process.pid == o.leader.pid {
				continue
			}
			if processUsesRuntimeDirAt(procRoot, process.pid, o.dir) {
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

// StopRecordedProcess stops only a verified recorded group.
func StopRecordedProcess(
	ctx context.Context,
	pid int,
	dir string,
	grace time.Duration,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, contextutil.ErrorWithCause(ctx)
	}
	owner, ok := newProcessGroupOwner(pid, dir)
	if !ok || !owner.refresh() {
		return false, nil
	}
	proc := NewGroup(nil, pid)
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		slog.Debug("session: terminate stale process group", "pid", pid, "error", err)
	}

	graceCtx, cancel := context.WithTimeout(ctx, grace)
	waitErr := waitForOwnedProcessGroup(graceCtx, owner)
	cancel()
	if waitErr == nil {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, contextutil.ErrorWithCause(ctx)
	}
	if !errors.Is(waitErr, context.DeadlineExceeded) {
		return false, waitErr
	}
	if !ProcessGroupAlive(pid) || !owner.refresh() {
		return true, nil
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		slog.Debug("session: kill stale process group", "pid", pid, "error", err)
	}
	postKillCtx, cancel := context.WithTimeout(ctx, grace)
	_ = waitForOwnedProcessGroup(postKillCtx, owner)
	cancel()
	if err := ctx.Err(); err != nil {
		return false, contextutil.ErrorWithCause(ctx)
	}
	return true, nil
}

func waitForOwnedProcessGroup(ctx context.Context, owner *processGroupOwner) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return contextutil.ErrorWithCause(ctx)
		}
		if !ProcessGroupAlive(owner.pgid) || !owner.refresh() {
			return nil
		}
		select {
		case <-ctx.Done():
			return contextutil.ErrorWithCause(ctx)
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
