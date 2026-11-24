//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

const workflowControlFrameLimit = 1024

type workflowRequest struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
}

type workflowReply struct {
	ID                string        `json:"id"`
	State             workflowState `json:"state"`
	Accepted          bool          `json:"accepted"`
	LastCompletedStep int           `json:"last_completed_step"`
	CurrentStep       int           `json:"current_step,omitempty"`
	CurrentStepPhase  string        `json:"current_step_phase,omitempty"`
	TerminalError     string        `json:"terminal_error,omitempty"`
}

func workflowReplyFor(status workflowStatus, accepted bool) workflowReply {
	return workflowReply{
		ID:                status.ID,
		State:             status.State,
		Accepted:          accepted,
		LastCompletedStep: status.LastCompletedStep,
		CurrentStep:       status.CurrentStep,
		CurrentStepPhase:  status.CurrentStepPhase,
		TerminalError:     status.TerminalError,
	}
}

func workflowRegistry() (*os.File, error) {
	path := fmt.Sprintf("/tmp/pf-workflows-%d", os.Geteuid())
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	return namedOpenDir(unix.AT_FDCWD, path)
}

type workflowControl struct {
	owner          *namedOwner
	workflow       *workflowLifecycle
	socketIdentity unix.Stat_t
	done           chan struct{}
	once           sync.Once
	closeErr       error
}

func startWorkflowControl(w *workflowLifecycle) (*workflowControl, error) {
	root, err := workflowRegistry()
	if err != nil {
		return nil, err
	}
	return startWorkflowControlIn(root, w)
}

// Takes ownership of root. Stale reservations are removed only through the
// existing PID/start-time and inode-checked named-session recovery path.
func startWorkflowControlIn(root *os.File, w *workflowLifecycle) (*workflowControl, error) {
	id, _, _ := w.snapshot()
	if !sessionNamePattern.MatchString(id) {
		root.Close()
		return nil, errors.New("invalid workflow ID")
	}
	if err := namedRecover(root, id); err != nil && !errors.Is(err, os.ErrNotExist) {
		root.Close()
		return nil, fmt.Errorf("reserve workflow ID: %w", err)
	}
	owner, err := namedReserveIn(root, id)
	if err != nil {
		return nil, fmt.Errorf("reserve workflow ID: %w", err)
	}
	if err := owner.publish(); err != nil {
		owner.close()
		return nil, fmt.Errorf("publish workflow control: %w", err)
	}
	var socketIdentity unix.Stat_t
	if err := unix.Fstatat(int(owner.dir.Fd()), "control", &socketIdentity, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		owner.close()
		return nil, fmt.Errorf("inspect workflow control: %w", err)
	}
	control := &workflowControl{owner: owner, workflow: w, socketIdentity: socketIdentity, done: make(chan struct{})}
	go func() {
		defer close(control.done)
		for {
			conn, err := owner.listener.AcceptUnix()
			if err != nil {
				return
			}
			workflowHandle(conn, w)
			_ = conn.Close()
		}
	}()
	return control, nil
}

func (c *workflowControl) close() error {
	c.once.Do(func() {
		if c.owner.listener != nil {
			c.closeErr = errors.Join(c.closeErr, c.owner.listener.Close())
		}
		<-c.done
		c.closeErr = errors.Join(c.closeErr, workflowWriteTerminalStatus(c.owner.dir, c.workflow.status()))
		var current unix.Stat_t
		if err := unix.Fstatat(int(c.owner.dir.Fd()), "control", &current, unix.AT_SYMLINK_NOFOLLOW); err == nil {
			if sameWorkflowSocket(c.socketIdentity, current) {
				c.closeErr = errors.Join(c.closeErr, unix.Unlinkat(int(c.owner.dir.Fd()), "control", 0))
			} else {
				c.closeErr = errors.Join(c.closeErr, errors.New("workflow control socket changed during cleanup"))
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			c.closeErr = errors.Join(c.closeErr, err)
		}
		c.closeErr = errors.Join(c.closeErr, c.owner.dir.Close(), c.owner.root.Close())
	})
	return c.closeErr
}

func workflowWriteTerminalStatus(dir *os.File, status workflowStatus) error {
	fd, err := unix.Openat(int(dir.Fd()), "status.tmp", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "status.tmp")
	defer unix.Unlinkat(int(dir.Fd()), "status.tmp", 0)
	if err = json.NewEncoder(f).Encode(status); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return unix.Renameat2(int(dir.Fd()), "status.tmp", int(dir.Fd()), "status", unix.RENAME_NOREPLACE)
}

func workflowReadTerminalStatus(dir *os.File, id string) (workflowStatus, error) {
	var status workflowStatus
	fd, err := unix.Openat(int(dir.Fd()), "status", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return status, err
	}
	f := os.NewFile(uintptr(fd), "status")
	defer f.Close()
	if err := namedCheck(fd, unix.S_IFREG, 0600, true); err != nil {
		return status, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return status, err
	}
	if len(data) > 4096 {
		return status, errors.New("oversized workflow status")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		return status, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return status, errors.New("terminal workflow status must contain one JSON object")
	}
	if status.ID != id || status.LastCompletedStep < 0 || status.CurrentStep < 0 {
		return status, errors.New("invalid terminal workflow status")
	}
	if status.CurrentStep == 0 {
		if status.CurrentStepPhase != "" {
			return status, errors.New("terminal workflow phase has no current step")
		}
	} else if status.CurrentStep != status.LastCompletedStep+1 {
		return status, errors.New("terminal workflow steps are inconsistent")
	}
	switch status.State {
	case workflowStopped, workflowFailed, workflowCompleted:
	default:
		return status, errors.New("workflow status is not terminal")
	}
	if status.CurrentStep != 0 {
		switch status.CurrentStepPhase {
		case "waiting", "running", "failed", "canceled":
		default:
			return status, errors.New("invalid terminal workflow phase")
		}
	}
	switch status.TerminalError {
	case "", "canceled", "deadline_exceeded", "operation_failed":
	default:
		return status, errors.New("invalid workflow error category")
	}
	return status, nil
}

// One bounded request per connection; control messages are same-user only.
func workflowHandle(conn *net.UnixConn, w *workflowLifecycle) {
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	peer, err := namedPeer(conn)
	if err != nil || peer.Uid != uint32(os.Geteuid()) {
		return
	}
	req, err := workflowReadRequest(conn)
	if err != nil {
		return
	}
	reply, err := workflowApply(w, req)
	if err != nil {
		return
	}
	if req.Operation == "stop" && reply.Accepted {
		workflowWaitTerminal(w, 5*time.Second)
		reply = workflowReplyFor(w.status(), true)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	_ = json.NewEncoder(conn).Encode(reply)
}

func workflowReadRequest(reader io.Reader) (workflowRequest, error) {
	var req workflowRequest
	line, err := bufio.NewReader(io.LimitReader(reader, workflowControlFrameLimit+1)).ReadBytes('\n')
	if err != nil {
		return req, err
	}
	if len(line) > workflowControlFrameLimit {
		return req, errors.New("workflow request too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errors.New("workflow request must contain one JSON object")
	}
	return req, nil
}

func workflowApply(w *workflowLifecycle, req workflowRequest) (workflowReply, error) {
	if req.ID != w.status().ID {
		return workflowReply{}, errors.New("workflow ID mismatch")
	}
	accepted := true
	switch req.Operation {
	case "status":
	case "pause":
		accepted = w.pause()
	case "resume":
		accepted = w.resume()
	case "stop":
		accepted = w.stop()
	default:
		return workflowReply{}, errors.New("unknown workflow operation")
	}
	return workflowReplyFor(w.status(), accepted), nil
}

func workflowWaitTerminal(w *workflowLifecycle, limit time.Duration) {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		status, changed := w.statusAndChange()
		switch status.State {
		case workflowStopped, workflowFailed, workflowCompleted:
			return
		}
		select {
		case <-changed:
		case <-timer.C:
			return
		}
	}
}

func sameWorkflowSocket(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Nlink == b.Nlink
}

func workflowCall(ctx context.Context, root *os.File, id, operation string) (workflowReply, error) {
	var reply workflowReply
	if !sessionNamePattern.MatchString(id) {
		return reply, errors.New("invalid workflow ID")
	}
	dir, err := namedOpenDir(int(root.Fd()), id)
	if err != nil {
		return reply, err
	}
	defer dir.Close()
	rec, err := namedRead(dir)
	if err != nil {
		return reply, fmt.Errorf("workflow not ready or stale: %w", err)
	}
	if rec.Name != id {
		return reply, errors.New("workflow name mismatch")
	}
	var endpoint unix.Stat_t
	if err = unix.Fstatat(int(dir.Fd()), "control", &endpoint, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			status, statusErr := workflowReadTerminalStatus(dir, id)
			if statusErr == nil {
				return workflowReplyFor(status, operation == "status"), nil
			}
		}
		return reply, err
	}
	if endpoint.Mode != unix.S_IFSOCK|0600 || endpoint.Uid != uint32(os.Geteuid()) || endpoint.Nlink != 1 {
		return reply, errors.New("unsafe workflow socket")
	}
	start, err := namedProcStart(rec.PID)
	if err != nil || start != rec.Start {
		return reply, errNamedStale
	}
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "unix", namedFDPath(dir, "control"))
	if err != nil {
		return reply, err
	}
	defer conn.Close()
	client := conn.(*net.UnixConn)
	deadline := time.Now().Add(6 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = client.SetDeadline(deadline)
	cancelWatch := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer cancelWatch()
	peer, err := namedPeer(client)
	if err != nil || peer.Uid != uint32(os.Geteuid()) || int(peer.Pid) != rec.PID {
		return reply, errors.New("workflow owner authentication failed")
	}
	start, err = namedProcStart(rec.PID)
	if err != nil || start != rec.Start {
		return reply, errors.New("workflow owner identity changed")
	}
	var current unix.Stat_t
	if err = unix.Fstatat(int(dir.Fd()), "control", &current, unix.AT_SYMLINK_NOFOLLOW); err != nil || !sameWorkflowSocket(endpoint, current) {
		return reply, errors.New("workflow endpoint changed")
	}
	if err = ctx.Err(); err != nil {
		return reply, err
	}
	if err = json.NewEncoder(client).Encode(workflowRequest{ID: id, Operation: operation}); err != nil {
		return reply, err
	}
	decoder := json.NewDecoder(io.LimitReader(client, workflowControlFrameLimit+1))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&reply); err != nil {
		return reply, err
	}
	if reply.ID != id {
		return workflowReply{}, errors.New("workflow reply ID mismatch")
	}
	return reply, ctx.Err()
}

func workflowCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "workflow",
		Short: "Control an active same-user script",
		Long: `Control a script started with pf run --workflow-id ID.

The control socket and status record live below /tmp/pf-workflows-<uid> and
are private to the current user. Pause is cooperative: status first reports
pause-requested and becomes paused only after the current command reaches the
next script boundary. Stop cancels in-flight context-aware work and waits up to
five seconds for the run and Session cleanup; if it is still stopping, query
status again. Status JSON includes the stable ID, state, last completed step,
current step/phase, and a sanitized terminal error category. It never includes
script contents or typed input. A terminal stopped/failed/completed state can
be read after the socket closes. A process crash has no trustworthy terminal
status, and an unknown or stale endpoint fails closed.

These controls do not reverse operations already delivered to the desktop and
do not provide a global hotkey or process supervisor.`,
	}
	for _, operation := range []string{"status", "pause", "resume", "stop"} {
		var output string
		short := operation + " an active workflow"
		if operation == "status" {
			short = "show the status of an active workflow"
		}
		cmd := &cobra.Command{Use: operation + " ID", Short: short, Args: cobra.ExactArgs(1), Example: "  pf workflow " + operation + " task-1", RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := parseOutputMode(output)
			if err != nil {
				return err
			}
			root, err := workflowRegistry()
			if err != nil {
				return err
			}
			defer root.Close()
			reply, err := workflowCall(cmd.Context(), root, args[0], operation)
			if err != nil {
				return err
			}
			if mode == outputModeJSON {
				err = json.NewEncoder(cmd.OutOrStdout()).Encode(reply)
			} else {
				err = writeCLIOutput(cmd.OutOrStdout(), "%s %s\n", reply.ID, reply.State)
			}
			if err != nil {
				return err
			}
			if !reply.Accepted {
				return errors.New("workflow operation not accepted")
			}
			return nil
		}}
		cmd.Flags().StringVar(&output, "output", "plain", "plain|json")
		group.AddCommand(cmd)
	}
	return group
}
