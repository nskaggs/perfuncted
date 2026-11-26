package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var errInvalidArgument = errors.New("invalid argument")

func TestNilAndZeroManagedProcessLifecyclesAreSafe(t *testing.T) {
	var managed *SessionProcess
	if err := managed.Wait(context.Background(), errInvalidArgument); err != nil {
		t.Fatalf("wait on nil managed process = %v, want nil", err)
	}
	if !managed.WaitTimeout(0) {
		t.Fatal("nil managed process was not treated as complete")
	}
	managed.Stop(0)
	if NewSessionProcess(nil) != nil || NewSessionProcess(&exec.Cmd{}) != nil {
		t.Fatal("unstarted command produced a managed process")
	}

	var proc *Group
	if proc.GroupAlive() || ProcessGroupAlive(0) {
		t.Fatal("invalid process group was reported alive")
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal nil process = %v, want nil", err)
	}
	if err := proc.WaitGroup(nil, errInvalidArgument); err != nil {
		t.Fatalf("wait nil process group = %v, want nil", err)
	}
	proc.Stop(0)
	zero := &Group{}
	if err := zero.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal zero process group = %v, want nil", err)
	}
	if err := zero.WaitGroup(context.Background(), errInvalidArgument); err != nil {
		t.Fatalf("wait zero process group = %v, want nil", err)
	}
	if err := zero.WaitGroup(nil, errInvalidArgument); err != nil {
		t.Fatalf("wait zero process group with nil context = %v, want nil", err)
	}
	if !zero.waitGroupTimeout(0) {
		t.Fatal("zero process group did not complete immediately")
	}

	if contextutil.ErrorWithCause(nil) != nil || contextutil.ErrorWithCause(context.Background()) != nil {
		t.Fatal("inactive cancellation context produced an error")
	}
	cause := errors.New("session shutdown requested")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := contextutil.ErrorWithCause(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("cancellation error = %v, want context and cause", err)
	}
}

// If the process has exited and the caller has already canceled, the wait
// must never randomly report a successful observation from the ready child.
func TestManagedSessionProcessWaitCompletedChildRespectsCancellation(t *testing.T) {
	done := make(chan struct{})
	close(done)
	proc := &SessionProcess{waitDone: done}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for n := 0; n < 300; n++ {
		if err := proc.Wait(ctx, errInvalidArgument); !errors.Is(err, context.Canceled) {
			t.Fatalf("wait %d acknowledged completed child despite canceled caller: %v", n, err)
		}
	}
}

func TestManagedSessionProcessWaitPreservesCancellationCause(t *testing.T) {
	done := make(chan struct{})
	close(done)
	proc := &SessionProcess{waitDone: done}
	cause := errors.New("synthetic owner revoked child observation")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := proc.Wait(ctx, errInvalidArgument); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("process wait lost cancellation cause: %v", err)
	}
}

// Waiting for a live process group must respect cancellation, preserve the
// caller's cancellation cause, and never terminate a group merely because
// its observer stopped waiting. Only explicit shutdown may signal children.
func TestManagedProcessGroupWaitCancellationPreservesRunningProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proc := &Group{cmd: cmd, pid: cmd.Process.Pid}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	if !proc.GroupAlive() {
		t.Fatal("newly started process group not alive")
	}
	if err := proc.WaitGroup(nil, errInvalidArgument); !errors.Is(err, errInvalidArgument) {
		t.Fatalf("nil wait context: %v", err)
	}
	cause := errors.New("shutdown observer canceled")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := proc.WaitGroup(ctx, errInvalidArgument); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("pre-canceled wait lost cause: %v", err)
	}
	ctx, deadlineCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer deadlineCancel()
	if err := proc.WaitGroup(ctx, errInvalidArgument); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live group waiter missed deadline: %v", err)
	}
	if !proc.GroupAlive() {
		t.Fatal("canceled observation terminated process group")
	}
	if proc.waitGroupTimeout(0) {
		t.Fatal("live group reported complete with zero timeout")
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("explicit shutdown failed: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed helper reported successful exit")
	}
	if err := proc.WaitGroup(context.Background(), errInvalidArgument); err != nil {
		t.Fatalf("reaped group not marked gone: %v", err)
	}
	if !proc.waitGroupTimeout(0) {
		t.Fatal("reaped group reported live")
	}
}

// Observe the precise point at which wait starts listening for cancellation.
// The child never exits, so the only permitted completion is caller revocation.
type enteringWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *enteringWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestManagedSessionProcessWaitRevokedWhileChildStillRunning(t *testing.T) {
	for attempt := 0; attempt < 30; attempt++ {
		childExited := make(chan struct{})
		process := &SessionProcess{waitDone: childExited}
		cause := errors.New("session ownership was revoked")
		parent, cancel := context.WithCancelCause(context.Background())
		observed := &enteringWaitContext{Context: parent, entered: make(chan struct{})}
		result := make(chan error, 1)
		go func() { result <- process.Wait(observed, errInvalidArgument) }()
		select {
		case <-observed.entered:
		case <-time.After(time.Second):
			cancel(cause)
			t.Fatal("process wait did not enter its cancellation selector")
		}
		cancel(cause)
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("wait attempt %d lost cancellation or cause: %v", attempt, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("wait attempt %d remained blocked after cancellation", attempt)
		}
		select {
		case <-childExited:
			t.Fatal("canceling wait modified the child completion signal")
		default:
		}
	}
}

func TestProcessUsesRuntimeDirAt(t *testing.T) {
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "123")
	if err := os.Mkdir(pidDir, 0o700); err != nil {
		t.Fatalf("mkdir process dir: %v", err)
	}
	environPath := filepath.Join(pidDir, "environ")

	writeEnvironment := func(values ...string) {
		t.Helper()
		if err := os.WriteFile(environPath, []byte(strings.Join(values, "\x00")+"\x00"), 0o600); err != nil {
			t.Fatalf("write environ: %v", err)
		}
	}

	writeEnvironment("PATH=/usr/bin", "XDG_RUNTIME_DIR=/tmp/perfuncted-xdg-owned")
	if !processUsesRuntimeDirAt(procRoot, 123, "/tmp/perfuncted-xdg-owned") {
		t.Fatal("matching runtime directory was not recognized")
	}
	if processUsesRuntimeDirAt(procRoot, 123, "/tmp/perfuncted-xdg-other") {
		t.Fatal("mismatched runtime directory was accepted")
	}

	writeEnvironment("PATH=/usr/bin", "OTHER=/tmp/perfuncted-xdg-owned")
	if processUsesRuntimeDirAt(procRoot, 123, "/tmp/perfuncted-xdg-owned") {
		t.Fatal("runtime directory value under another key was accepted")
	}
	if processUsesRuntimeDirAt(procRoot, 999, "/tmp/perfuncted-xdg-owned") {
		t.Fatal("missing process was accepted")
	}
}

// A process can exit between directory enumeration and stat inspection.
// Missing metadata must not be interpreted as an owned live process.
func TestProcessSnapshotRejectsDisappearedStatFile(t *testing.T) {
	root := t.TempDir()
	processDir := filepath.Join(root, "391")
	if err := os.Mkdir(processDir, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readProcessSnapshotAt(root, 391)
	if !errors.Is(err, os.ErrNotExist) || snapshot != (processSnapshot{}) {
		t.Fatalf("disappeared process metadata acquired identity: %+v err=%v", snapshot, err)
	}
}

func syntheticProcStat(pid int, comm, pgid, startTime string) string {
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "S"
	fields[1] = "1"
	fields[2] = pgid
	fields[19] = startTime
	return fmt.Sprintf("%d (%s) %s\n", pid, comm, strings.Join(fields, " "))
}

//nolint:unparam // Synthetic fixture preserves the interface/argument contract.
func writeSyntheticProcProcess(t *testing.T, root string, pid int, comm string, pgid int, startTime uint64, runtime string) {
	t.Helper()
	processDir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(processDir, 0o700); err != nil {
		t.Fatalf("create synthetic process directory: %v", err)
	}
	stat := syntheticProcStat(pid, comm, strconv.Itoa(pgid), strconv.FormatUint(startTime, 10))
	if err := os.WriteFile(filepath.Join(processDir, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatalf("write synthetic process stat: %v", err)
	}
	environ := "PATH=/usr/bin\x00"
	if runtime != "" {
		environ += "XDG_RUNTIME_DIR=" + runtime + "\x00"
	}
	if err := os.WriteFile(filepath.Join(processDir, "environ"), []byte(environ), 0o600); err != nil {
		t.Fatalf("write synthetic process environment: %v", err)
	}
}

func TestStaleMalformedPIDThresholdNeverTrustsShortAge(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		maxAge time.Duration
		want   time.Duration
	}{
		{name: "short caller age", maxAge: time.Second, want: noPIDFileReapGrace},
		{name: "minimum boundary", maxAge: noPIDFileReapGrace, want: noPIDFileReapGrace},
		{name: "long caller age", maxAge: 10 * time.Minute, want: 10 * time.Minute},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := staleMalformedPIDThreshold(testCase.maxAge); got != testCase.want {
				t.Fatalf("stale malformed PID threshold = %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestReadProcessSnapshotRejectsMalformedIdentity(t *testing.T) {
	root := t.TempDir()
	if _, err := readProcessSnapshotAt("", 1); err == nil {
		t.Fatal("empty proc root produced a process identity")
	}
	if _, err := readProcessSnapshotAt(root, 0); err == nil {
		t.Fatal("nonpositive PID produced a process identity")
	}
	if len(processGroupMembersAt("", 1)) != 0 || len(processGroupMembersAt(root, 0)) != 0 {
		t.Fatal("invalid process-group inputs returned members")
	}

	processDir := filepath.Join(root, "73")
	if err := os.Mkdir(processDir, 0o700); err != nil {
		t.Fatalf("create synthetic process directory: %v", err)
	}
	statPath := filepath.Join(processDir, "stat")
	testCases := []struct {
		name  string
		stat  string
		match string
	}{
		{name: "missing command delimiter", stat: "73 worker S 1 73"},
		{name: "truncated fields", stat: "73 (worker) S 1 73"},
		{name: "invalid group", stat: syntheticProcStat(73, "worker", "not-a-pgid", "9"), match: "invalid process group"},
		{name: "nonpositive group", stat: syntheticProcStat(73, "worker", "0", "9"), match: "invalid process group"},
		{name: "invalid start time", stat: syntheticProcStat(73, "worker", "73", "not-a-start-time"), match: "invalid process start time"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := os.WriteFile(statPath, []byte(testCase.stat), 0o600); err != nil {
				t.Fatalf("write synthetic stat: %v", err)
			}
			if _, err := readProcessSnapshotAt(root, 73); err == nil {
				t.Fatal("malformed process stat produced an identity")
			} else if testCase.match != "" && !strings.Contains(err.Error(), testCase.match) {
				t.Fatalf("process stat error = %v, want %q", err, testCase.match)
			}
		})
	}
}

func TestProcessGroupOwnerTracksOnlyEstablishedProcessIdentities(t *testing.T) {
	const (
		runtime  = "/runtime/perfuncted"
		leaderID = 410
		childID  = 411
	)
	root := t.TempDir()
	writeSyntheticProcProcess(t, root, leaderID, "leader) with paren", leaderID, 17, runtime)
	writeSyntheticProcProcess(t, root, childID, "child", leaderID, 22, runtime)
	writeSyntheticProcProcess(t, root, 412, "malformed", leaderID, 1, runtime)
	if err := os.WriteFile(filepath.Join(root, "412", "stat"), []byte("not a process stat"), 0o600); err != nil {
		t.Fatalf("write malformed stat: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "not-a-pid"), 0o700); err != nil {
		t.Fatalf("write non-PID proc entry: %v", err)
	}

	members := processGroupMembersAt(root, leaderID)
	if len(members) != 2 {
		t.Fatalf("synthetic process group members = %+v, want leader and child only", members)
	}

	owner := &processGroupOwner{
		pgid:    leaderID,
		dir:     runtime,
		leader:  processSnapshot{pid: leaderID, pgid: leaderID, startTime: 17},
		members: make(map[processSnapshot]struct{}),
	}
	if !owner.refreshAt(root) {
		t.Fatal("owned leader did not establish its process-group ownership")
	}
	if _, tracked := owner.members[processSnapshot{pid: childID, pgid: leaderID, startTime: 22}]; !tracked {
		t.Fatal("owned descendant was not recorded while its leader was present")
	}

	if err := os.RemoveAll(filepath.Join(root, strconv.Itoa(leaderID))); err != nil {
		t.Fatalf("remove exited leader: %v", err)
	}
	if !owner.refreshAt(root) {
		t.Fatal("known owned descendant lost group ownership when its leader exited")
	}
	writeSyntheticProcProcess(t, root, childID, "child", leaderID, 23, runtime)
	if owner.refreshAt(root) {
		t.Fatal("reused child PID inherited the previous process identity")
	}

	writeSyntheticProcProcess(t, root, leaderID, "reused leader", leaderID, 18, runtime)
	if owner.refreshAt(root) {
		t.Fatal("reused leader PID inherited the previous process-group ownership")
	}

	writeSyntheticProcProcess(t, root, leaderID, "foreign leader", leaderID, 30, "/runtime/other")
	writeSyntheticProcProcess(t, root, childID, "untracked child", leaderID, 31, runtime)
	foreignOwner := &processGroupOwner{
		pgid:    leaderID,
		dir:     runtime,
		leader:  processSnapshot{pid: leaderID, pgid: leaderID, startTime: 30},
		members: make(map[processSnapshot]struct{}),
	}
	if foreignOwner.refreshAt(root) {
		t.Fatal("untracked child established ownership for a foreign leader")
	}
	if (&processGroupOwner{dir: runtime}).refresh() {
		t.Fatal("invalid group leader unexpectedly established ownership")
	}
}
