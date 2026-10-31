package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestManagedSessionProcessWaitCancellationKeepsReapingChild(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+readyPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start managed process: %v", err)
	}
	process := newManagedSessionProcess(cmd)
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })

	if err := waitForFile(context.Background(), readyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for helper readiness: %v", err)
	}
	if process.waitTimeout(-time.Second) {
		t.Fatal("negative timeout reported an unfinished child as exited")
	}

	if err := process.wait(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("wait with nil context = %v, want ErrInvalidArgument", err)
	}
	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := process.wait(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait with canceled context = %v, want context.Canceled", err)
	}
	if process.waitTimeout(0) {
		t.Fatal("canceled waiter released ownership before the child was reaped")
	}

	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("terminate managed process group: %v", err)
	}
	completionCtx, completionCancel := context.WithTimeout(context.Background(), time.Second)
	defer completionCancel()
	if err := process.wait(completionCtx); err == nil {
		t.Fatal("reaped helper process unexpectedly returned a successful exit")
	}
	if !process.waitTimeout(0) {
		t.Fatal("managed process did not publish completed wait")
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

func TestNilAndZeroManagedProcessLifecyclesAreSafe(t *testing.T) {
	var managed *managedSessionProcess
	if err := managed.wait(context.Background()); err != nil {
		t.Fatalf("wait on nil managed process = %v, want nil", err)
	}
	if !managed.waitTimeout(0) {
		t.Fatal("nil managed process was not treated as complete")
	}
	managed.stop(0)
	if newManagedSessionProcess(nil) != nil || newManagedSessionProcess(&exec.Cmd{}) != nil {
		t.Fatal("unstarted command produced a managed process")
	}

	var proc *managedProc
	if proc.groupAlive() || processGroupAlive(0) {
		t.Fatal("invalid process group was reported alive")
	}
	if err := proc.signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal nil process = %v, want nil", err)
	}
	if err := proc.waitGroup(nil); err != nil {
		t.Fatalf("wait nil process group = %v, want nil", err)
	}
	proc.stop(0)
	zero := &managedProc{}
	if err := zero.signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal zero process group = %v, want nil", err)
	}
	if err := zero.waitGroup(context.Background()); err != nil {
		t.Fatalf("wait zero process group = %v, want nil", err)
	}
	if err := zero.waitGroup(nil); err != nil {
		t.Fatalf("wait zero process group with nil context = %v, want nil", err)
	}
	if !zero.waitGroupTimeout(0) {
		t.Fatal("zero process group did not complete immediately")
	}

	if contextErrorWithCause(nil) != nil || contextErrorWithCause(context.Background()) != nil {
		t.Fatal("inactive cancellation context produced an error")
	}
	cause := errors.New("session shutdown requested")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := contextErrorWithCause(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("cancellation error = %v, want context and cause", err)
	}
}

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
