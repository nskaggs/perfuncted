package perfuncted

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	cleanupStaleSessionsMu sync.Mutex
	lastCleanupTime        time.Time
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
	// Take only the rate-limit decision under the lock. The scan itself can
	// take hundreds of milliseconds (per-process termination waits,
	// fusermount subprocesses); holding the mutex across it would stall
	// concurrent Open calls that merely need to know whether cleanup is
	// due. Overlapping scans are impossible within the rate interval and
	// benign beyond it — reapSessionDir is idempotent per directory.
	cleanupStaleSessionsMu.Lock()
	if time.Since(lastCleanupTime) < cleanupStaleMinInterval {
		cleanupStaleSessionsMu.Unlock()
		return
	}
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()

	matches, err := filepath.Glob(nestedSessionPattern())
	if err != nil {
		slog.Warn("unable to glob nested sessions", "error", err)
		return
	}
	now := time.Now()
	for _, d := range matches {
		pidPath := filepath.Join(d, sessionOwnerPIDFile)
		data, err := os.ReadFile(pidPath)
		if err != nil {
			fi, statErr := os.Stat(d)
			if statErr != nil {
				continue
			}
			if now.Sub(fi.ModTime()) > noPIDFileReapGrace {
				reapSessionDir(d)
			}
			continue
		}
		pidStr := strings.TrimSpace(string(data))
		pid, perr := strconv.Atoi(pidStr)
		if perr != nil {
			fi, statErr := os.Stat(d)
			if statErr == nil && now.Sub(fi.ModTime()) > staleMalformedPIDThreshold(maxAge) {
				reapSessionDir(d)
			}
			continue
		}
		if !pidAlive(pid) {
			reapSessionDir(d)
			continue
		}
	}
}

func reapSessionDir(dir string) {
	if !isManagedSessionDir(dir) {
		slog.Debug("session: skip stale directory with unexpected path", "path", dir)
		return
	}
	for _, name := range sessionChildPIDFiles {
		pid, err := readPIDFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		// Stale-owner cleanup is an independent maintenance action. Its short
		// grace does not bound active Session process-stop operations.
		if !stopRecordedProcess(pid, dir, 100*time.Millisecond) {
			slog.Debug(
				"session: skip stale child with mismatched runtime directory",
				"pid", pid,
				"path", dir,
			)
			continue
		}
	}
	unmountSubdirs(dir)
	if err := os.RemoveAll(dir); err != nil {
		slog.Debug("session: reap remove dir", "path", dir, "error", err)
	}
}

func unmountSubdirs(dir string) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return
	}
	prefix := dir + "/"
	var mounts []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mountpoint := fields[4]
		if strings.HasPrefix(mountpoint, prefix) || mountpoint == dir {
			mounts = append(mounts, mountpoint)
		}
	}
	for i := len(mounts) - 1; i >= 0; i-- {
		exec.Command("fusermount", "-u", mounts[i]).Run() //nolint:errcheck
	}
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

func stopRecordedProcess(pid int, dir string, grace time.Duration) bool {
	owner, ok := newProcessGroupOwner(pid, dir)
	if !ok || !owner.refresh() {
		return false
	}
	proc := &managedProc{pid: pid}
	if err := proc.signal(syscall.SIGTERM); err != nil {
		slog.Debug("session: terminate stale process group", "pid", pid, "error", err)
	}

	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !processGroupAlive(pid) {
			return true
		}
		if !owner.refresh() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !processGroupAlive(pid) || !owner.refresh() {
		return true
	}
	if err := proc.signal(syscall.SIGKILL); err != nil {
		slog.Debug("session: kill stale process group", "pid", pid, "error", err)
	}
	_ = proc.waitGroupTimeout(grace)
	return true
}

func staleMalformedPIDThreshold(maxAge time.Duration) time.Duration {
	if maxAge < noPIDFileReapGrace {
		return noPIDFileReapGrace
	}
	return maxAge
}
