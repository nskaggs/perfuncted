//go:build linux

package sessionregistry

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

	"github.com/nskaggs/perfuncted/v2/internal/workflow"
	"golang.org/x/sys/unix"
)

const workflowControlFrameLimit = 1024

type workflowRequest struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
}

type WorkflowReply struct {
	ID                string         `json:"id"`
	State             workflow.State `json:"state"`
	Accepted          bool           `json:"accepted"`
	LastCompletedStep int            `json:"last_completed_step"`
	CurrentStep       int            `json:"current_step,omitempty"`
	CurrentStepPhase  string         `json:"current_step_phase,omitempty"`
	TerminalError     string         `json:"terminal_error,omitempty"`
}

func workflowReplyFor(status workflow.Status, accepted bool) WorkflowReply {
	return WorkflowReply{
		ID:                status.ID,
		State:             status.State,
		Accepted:          accepted,
		LastCompletedStep: status.LastCompletedStep,
		CurrentStep:       status.CurrentStep,
		CurrentStepPhase:  status.CurrentStepPhase,
		TerminalError:     status.TerminalError,
	}
}

func WorkflowRegistry() (*os.File, error) {
	path := fmt.Sprintf("/tmp/pf-workflows-%d", os.Geteuid())
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	return OpenDir(unix.AT_FDCWD, path)
}

type WorkflowControl struct {
	owner          *Owner
	workflow       *workflow.Lifecycle
	socketIdentity unix.Stat_t
	done           chan struct{}
	once           sync.Once
	closeErr       error
}

func StartWorkflowControl(w *workflow.Lifecycle) (*WorkflowControl, error) {
	root, err := WorkflowRegistry()
	if err != nil {
		return nil, err
	}
	return startWorkflowControlIn(root, w)
}

// Takes ownership of root. Stale reservations are removed only through the
// existing PID/start-time and inode-checked named-session recovery path.
func startWorkflowControlIn(root *os.File, w *workflow.Lifecycle) (*WorkflowControl, error) {
	id, _, _ := w.Snapshot()
	if !sessionNamePattern.MatchString(id) {
		root.Close()
		return nil, errors.New("invalid workflow ID")
	}
	if err := Recover(root, id); err != nil && !errors.Is(err, os.ErrNotExist) {
		root.Close()
		return nil, fmt.Errorf("reserve workflow ID: %w", err)
	}
	owner, err := ReserveIn(root, id)
	if err != nil {
		return nil, fmt.Errorf("reserve workflow ID: %w", err)
	}
	if err := owner.Publish(); err != nil {
		owner.Close()
		return nil, fmt.Errorf("publish workflow control: %w", err)
	}
	var socketIdentity unix.Stat_t
	if err := unix.Fstatat(int(owner.dir.Fd()), "control", &socketIdentity, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		owner.Close()
		return nil, fmt.Errorf("inspect workflow control: %w", err)
	}
	control := &WorkflowControl{owner: owner, workflow: w, socketIdentity: socketIdentity, done: make(chan struct{})}
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

func (c *WorkflowControl) Close() error {
	c.once.Do(func() {
		c.closeErr = errors.Join(c.closeErr, workflowWriteTerminalStatus(c.owner.dir, c.workflow.Status()))
		if c.owner.listener != nil {
			c.closeErr = errors.Join(c.closeErr, c.owner.listener.Close())
		}
		<-c.done
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

func workflowWriteTerminalStatus(dir *os.File, status workflow.Status) error {
	fd, err := unix.Openat(int(dir.Fd()), "status.tmp", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "status.tmp")
	defer func() { _ = unix.Unlinkat(int(dir.Fd()), "status.tmp", 0) }()
	if err = json.NewEncoder(f).Encode(status); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if closeErr := f.Close(); closeErr != nil {
		return closeErr
	}
	return unix.Renameat2(int(dir.Fd()), "status.tmp", int(dir.Fd()), "status", unix.RENAME_NOREPLACE)
}

func workflowReadTerminalStatus(dir *os.File, id string) (workflow.Status, error) {
	var status workflow.Status
	fd, err := unix.Openat(int(dir.Fd()), "status", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return status, err
	}
	f := os.NewFile(uintptr(fd), "status")
	defer f.Close()
	if checkErr := namedCheck(fd, unix.S_IFREG, 0600, true); checkErr != nil {
		return status, checkErr
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
	if err := validateTerminalWorkflowStatus(status, id); err != nil {
		return status, err
	}
	return status, nil
}

// One bounded request per connection; control messages are same-user only.
func workflowHandle(conn *net.UnixConn, w *workflow.Lifecycle) {
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
		reply = workflowReplyFor(w.Status(), true)
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

func workflowApply(w *workflow.Lifecycle, req workflowRequest) (WorkflowReply, error) {
	if req.ID != w.Status().ID {
		return WorkflowReply{}, errors.New("workflow ID mismatch")
	}
	accepted := true
	switch req.Operation {
	case "status":
	case "pause":
		accepted = w.Pause()
	case "resume":
		accepted = w.Resume()
	case "stop":
		accepted = w.Stop()
	default:
		return WorkflowReply{}, errors.New("unknown workflow operation")
	}
	return workflowReplyFor(w.Status(), accepted), nil
}

func workflowWaitTerminal(w *workflow.Lifecycle, limit time.Duration) {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		status, changed := w.StatusAndChange()
		switch status.State {
		case workflow.Stopped, workflow.Failed, workflow.Completed:
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

func CallWorkflow(ctx context.Context, root *os.File, id, operation string) (WorkflowReply, error) {
	var reply WorkflowReply
	if err := ctx.Err(); err != nil {
		return reply, err
	}
	if !sessionNamePattern.MatchString(id) {
		return reply, errors.New("invalid workflow ID")
	}
	dir, err := OpenDir(int(root.Fd()), id)
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
	conn, endpoint, terminal, err := dialWorkflowEndpoint(ctx, dir, rec, id, operation)
	if err != nil {
		return reply, err
	}
	if terminal != nil {
		return *terminal, nil
	}
	defer conn.Close()
	client, ok := conn.(*net.UnixConn)
	if !ok {
		return reply, errors.New("unexpected workflow connection type")
	}
	deadline := time.Now().Add(6 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if deadlineErr := client.SetDeadline(deadline); deadlineErr != nil {
		return reply, deadlineErr
	}
	cancelWatch := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer cancelWatch()
	if err := authenticateWorkflowEndpoint(client, dir, rec, endpoint); err != nil {
		return reply, err
	}
	return exchangeWorkflowRequest(ctx, client, dir, id, operation)
}

func validateTerminalWorkflowStatus(status workflow.Status, id string) error {
	if status.ID != id || status.LastCompletedStep < 0 || status.CurrentStep < 0 {
		return errors.New("invalid terminal workflow status")
	}
	if status.CurrentStep == 0 {
		if status.CurrentStepPhase != "" {
			return errors.New("terminal workflow phase has no current step")
		}
	} else if status.CurrentStep != status.LastCompletedStep+1 {
		return errors.New("terminal workflow steps are inconsistent")
	}
	switch status.State {
	case workflow.Stopped, workflow.Failed, workflow.Completed:
	default:
		return errors.New("workflow status is not terminal")
	}
	if status.CurrentStep != 0 {
		switch status.CurrentStepPhase {
		case "waiting", "running", "failed", "canceled":
		default:
			return errors.New("invalid terminal workflow phase")
		}
	}
	switch status.TerminalError {
	case "", "canceled", "deadline_exceeded", "operation_failed":
	default:
		return errors.New("invalid workflow error category")
	}
	return nil
}

func dialWorkflowEndpoint(ctx context.Context, dir *os.File, rec Record, id, operation string) (net.Conn, unix.Stat_t, *WorkflowReply, error) {
	var endpoint unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), "control", &endpoint, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if reply, ok, terminalErr := recoverWorkflowReply(ctx, dir, id, operation); ok {
				return nil, endpoint, &reply, terminalErr
			}
		}
		return nil, endpoint, nil, err
	}
	if endpoint.Mode != unix.S_IFSOCK|0600 || endpoint.Uid != uint32(os.Geteuid()) || endpoint.Nlink != 1 {
		return nil, endpoint, nil, errors.New("unsafe workflow socket")
	}
	start, err := namedProcStart(rec.PID)
	if err != nil || start != rec.Start {
		return nil, endpoint, nil, errNamedStale
	}
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "unix", namedFDPath(dir, "control"))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, endpoint, nil, ctxErr
		}
		if errors.Is(err, unix.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
			if reply, ok, terminalErr := recoverWorkflowReply(ctx, dir, id, operation); ok {
				return nil, endpoint, &reply, terminalErr
			}
		}
		return nil, endpoint, nil, err
	}
	return conn, endpoint, nil, nil
}

func recoverWorkflowReply(ctx context.Context, dir *os.File, id, operation string) (WorkflowReply, bool, error) {
	status, err := workflowReadTerminalStatus(dir, id)
	if err != nil {
		return WorkflowReply{}, false, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return WorkflowReply{}, true, ctxErr
	}
	return workflowReplyFor(status, operation == "status"), true, nil
}

func authenticateWorkflowEndpoint(client *net.UnixConn, dir *os.File, rec Record, endpoint unix.Stat_t) error {
	peer, err := namedPeer(client)
	if err != nil || peer.Uid != uint32(os.Geteuid()) || int(peer.Pid) != rec.PID {
		return errors.New("workflow owner authentication failed")
	}
	start, err := namedProcStart(rec.PID)
	if err != nil || start != rec.Start {
		return errors.New("workflow owner identity changed")
	}
	var current unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), "control", &current, unix.AT_SYMLINK_NOFOLLOW); err != nil || !sameWorkflowSocket(endpoint, current) {
		return errors.New("workflow endpoint changed")
	}
	return nil
}

func exchangeWorkflowRequest(ctx context.Context, client *net.UnixConn, dir *os.File, id, operation string) (WorkflowReply, error) {
	var reply WorkflowReply
	if ctxErr := ctx.Err(); ctxErr != nil {
		return reply, ctxErr
	}
	if encodeErr := json.NewEncoder(client).Encode(workflowRequest{ID: id, Operation: operation}); encodeErr != nil {
		return reply, encodeErr
	}
	decoder := json.NewDecoder(io.LimitReader(client, workflowControlFrameLimit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return WorkflowReply{}, ctxErr
		}
		if operation == "status" && (errors.Is(err, io.EOF) || errors.Is(err, unix.ECONNRESET)) {
			if recovered, ok, terminalErr := recoverWorkflowReply(ctx, dir, id, operation); ok {
				return recovered, terminalErr
			}
		}
		return reply, err
	}
	if reply.ID != id {
		return WorkflowReply{}, errors.New("workflow reply ID mismatch")
	}
	return reply, ctx.Err()
}
