//go:build linux

// Package sessionregistry owns private same-user session and workflow registries.
package sessionregistry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var sessionNamePattern = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$")

type Record struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

type Reply struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	State       string            `json:"state"`
	Environment map[string]string `json:"environment,omitempty"`
}

type namedRequest struct {
	ID        string
	Operation string
}

type Owner struct {
	root     *os.File
	dir      *os.File
	record   Record
	listener *net.UnixListener
}

// Use fstat directly: os.FileInfo.Sys is syscall.Stat_t, not necessarily unix.Stat_t.
func namedCheck(fd int, kind uint32, perm uint32, single bool) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != kind || st.Mode&07777 != perm || (single && st.Nlink != 1) {
		return errors.New("unsafe named session registry entry")
	}
	return nil
}

func OpenDir(parent int, name string) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err = namedCheck(fd, unix.S_IFDIR, 0700, false); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func Registry() (*os.File, error) {
	// A fixed per-UID location under sticky /tmp is independent of the
	// caller's XDG_RUNTIME_DIR, including after attaching to another session.
	path := fmt.Sprintf("/tmp/pf-sessions-%d", os.Geteuid())
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	return OpenDir(unix.AT_FDCWD, path)
}

func namedProcStart(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid owner PID")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) <= 19 || fields[0] == "Z" {
		return "", errors.New("owner not live")
	}
	if _, err = strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", err
	}
	return fields[19], nil
}

func Reserve(name string) (*Owner, error) {
	if !sessionNamePattern.MatchString(name) {
		return nil, errors.New("invalid session name (1-32 letters, digits, underscores or hyphens)")
	}
	root, err := Registry()
	if err != nil {
		return nil, err
	}
	return ReserveIn(root, name)
}

// ReserveIn takes ownership of root, even on failure.
func ReserveIn(root *os.File, name string) (*Owner, error) {
	// Serialize creation through acquisition of the reservation lock.
	if err := unix.Flock(int(root.Fd()), unix.LOCK_EX); err != nil {
		root.Close()
		return nil, err
	}
	if err := unix.Mkdirat(int(root.Fd()), name, 0700); err != nil {
		root.Close()
		return nil, fmt.Errorf("reserve session name: %w", err)
	}
	dir, err := OpenDir(int(root.Fd()), name)
	if err != nil {
		root.Close()
		return nil, err
	}
	if lockErr := unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB); lockErr != nil {
		dir.Close()
		root.Close()
		return nil, lockErr
	}
	if err = unix.Flock(int(root.Fd()), unix.LOCK_UN); err != nil {
		dir.Close()
		root.Close()
		return nil, err
	}
	o := &Owner{root: root, dir: dir}
	o.record.Name = name
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		o.Close()
		return nil, err
	}
	o.record.ID = hex.EncodeToString(id[:])
	o.record.PID = os.Getpid()
	o.record.Start, err = namedProcStart(o.record.PID)
	if err != nil {
		o.Close()
		return nil, err
	}
	return o, nil
}

// Bind through the opened directory, not a mutable registry pathname.
func namedFDPath(dir *os.File, name string) string {
	return fmt.Sprintf("/proc/self/fd/%d/%s", dir.Fd(), name)
}

func (o *Owner) Close() {
	if o.listener != nil {
		o.listener.Close()
	}
	_ = unix.Unlinkat(int(o.dir.Fd()), "control", 0)
	_ = unix.Unlinkat(int(o.dir.Fd()), "record", 0)
	var owned, current unix.Stat_t
	if unix.Fstat(int(o.dir.Fd()), &owned) == nil &&
		unix.Fstatat(int(o.root.Fd()), o.record.Name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil &&
		owned.Ino == current.Ino && owned.Dev == current.Dev {
		_ = unix.Unlinkat(int(o.root.Fd()), o.record.Name, unix.AT_REMOVEDIR)
	}
	o.dir.Close()
	o.root.Close()
}

func namedPeer(c *net.UnixConn) (*unix.Ucred, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, err
	}
	var cred *unix.Ucred
	var sockErr error
	err = raw.Control(func(fd uintptr) { cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if err != nil {
		return nil, err
	}
	return cred, sockErr
}

func (o *Owner) Publish() error {
	path := namedFDPath(o.dir, "control")
	if len(path) > 107 {
		return errors.New("session control socket path too long")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	listener.SetUnlinkOnClose(false)
	o.listener = listener
	if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
		return chmodErr
	}
	// Publish metadata only after binding. An exclusive regular file in the
	// private reservation cannot overwrite a symlink or preexisting inode.
	fd, err := unix.Openat(int(o.dir.Fd()), "record.tmp", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "record")
	defer func() { _ = unix.Unlinkat(int(o.dir.Fd()), "record.tmp", 0) }()
	err = json.NewEncoder(f).Encode(o.record)
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return unix.Renameat2(int(o.dir.Fd()), "record.tmp", int(o.dir.Fd()), "record", unix.RENAME_NOREPLACE)
}

func namedRead(dir *os.File) (Record, error) {
	var rec Record
	fd, err := unix.Openat(int(dir.Fd()), "record", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return rec, err
	}
	f := os.NewFile(uintptr(fd), "record")
	defer f.Close()
	if checkErr := namedCheck(fd, unix.S_IFREG, 0600, true); checkErr != nil {
		return rec, checkErr
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return rec, err
	}
	if len(data) > 4096 {
		return rec, errors.New("oversized session record")
	}
	if decodeErr := json.Unmarshal(data, &rec); decodeErr != nil {
		return rec, decodeErr
	}
	id, err := hex.DecodeString(rec.ID)
	if err != nil || len(id) != 16 || !sessionNamePattern.MatchString(rec.Name) {
		return rec, errors.New("invalid session record")
	}
	return rec, nil
}

func (o *Owner) Serve(ctx context.Context, env map[string]string, stop context.CancelFunc, finished <-chan error) {
	defer stop()
	for {
		c, err := o.listener.AcceptUnix()
		if err != nil {
			return
		}
		halt := o.handle(ctx, c, env, stop, finished)
		c.Close()
		if halt {
			return
		}
	}
}

func (o *Owner) handle(ctx context.Context, c *net.UnixConn, env map[string]string, stop context.CancelFunc, finished <-chan error) bool {
	if c.SetDeadline(time.Now().Add(time.Second)) != nil {
		return false
	}
	peer, err := namedPeer(c)
	if err != nil || peer.Uid != uint32(os.Geteuid()) || ctx.Err() != nil {
		return false
	}
	var req namedRequest
	if json.NewDecoder(io.LimitReader(c, 4096)).Decode(&req) != nil || req.ID != o.record.ID {
		return false
	}
	reply := Reply{ID: o.record.ID, Name: o.record.Name, State: "ready"}
	switch req.Operation {
	case "inspect":
	case "attach":
		reply.Environment = env
	case "stop":
		stop()
		_ = c.SetWriteDeadline(time.Now().Add(30 * time.Second))
		select {
		case err := <-finished:
			if err != nil {
				return true
			}
			reply.State = "stopped"
		case <-time.After(25 * time.Second):
			return true
		}
	default:
		return false
	}
	// Never retry a stop, even if the response is lost.
	_ = json.NewEncoder(c).Encode(reply)
	return req.Operation == "stop"
}

var errNamedStale = errors.New("stale session owner")

// Recovery removes registry entries only, never signals a process or removes
// a desktop runtime. Existing session cleanup handles orphaned runtimes.
func Recover(root *os.File, name string) error {
	if !sessionNamePattern.MatchString(name) {
		return errors.New("invalid session name")
	}
	if err := namedCheck(int(root.Fd()), unix.S_IFDIR, 0700, false); err != nil {
		return err
	}
	if err := unix.Flock(int(root.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("registry busy: %w", err)
	}
	defer func() { _ = unix.Flock(int(root.Fd()), unix.LOCK_UN) }()
	dir, err := OpenDir(int(root.Fd()), name)
	if err != nil {
		return err
	}
	defer dir.Close()
	if lockErr := unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB); lockErr != nil {
		return fmt.Errorf("session reservation is active: %w", lockErr)
	}
	entries, err := dir.Readdirnames(-1)
	if err != nil {
		return err
	}
	artifacts, err := inspectNamedArtifacts(dir, entries)
	if err != nil {
		return err
	}
	if err := authenticateNamedRecovery(dir, name, artifacts); err != nil {
		return err
	}
	if err := recheckNamedArtifacts(root, dir, name, artifacts); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := unix.Unlinkat(int(dir.Fd()), entry, 0); err != nil {
			return err
		}
	}
	return unix.Unlinkat(int(root.Fd()), name, unix.AT_REMOVEDIR)
}

func Call(ctx context.Context, root *os.File, name, operation string) (Reply, error) {
	var reply Reply
	dir, err := OpenDir(int(root.Fd()), name)
	if err != nil {
		return reply, err
	}
	defer dir.Close()
	rec, err := namedRead(dir)
	if err != nil {
		return reply, fmt.Errorf("session not ready or stale: %w", err)
	}
	if rec.Name != name {
		return reply, errors.New("session name mismatch")
	}
	start, err := namedProcStart(rec.PID)
	if err != nil || start != rec.Start {
		return reply, errNamedStale
	}
	var st unix.Stat_t
	if statErr := unix.Fstatat(int(dir.Fd()), "control", &st, unix.AT_SYMLINK_NOFOLLOW); statErr != nil {
		return reply, statErr
	}
	if st.Mode != unix.S_IFSOCK|0600 || st.Uid != uint32(os.Geteuid()) {
		return reply, errors.New("unsafe session socket")
	}
	d := net.Dialer{Timeout: time.Second}
	conn, err := d.DialContext(ctx, "unix", namedFDPath(dir, "control"))
	if err != nil {
		return reply, err
	}
	defer conn.Close()
	c, ok := conn.(*net.UnixConn)
	if !ok {
		return reply, errors.New("unexpected session connection type")
	}
	if deadlineErr := c.SetDeadline(namedRequestDeadline(ctx, operation)); deadlineErr != nil {
		return reply, deadlineErr
	}
	cancelWatch := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer cancelWatch()
	if err := authenticateNamedPeer(c, rec); err != nil {
		return reply, err
	}
	return exchangeNamedRequest(ctx, c, rec, name, operation)
}

func SafeEnv(runtime map[string]string) map[string]string {
	return map[string]string{"XDG_RUNTIME_DIR": runtime["XDG_RUNTIME_DIR"], "WAYLAND_DISPLAY": runtime["WAYLAND_DISPLAY"], "DBUS_SESSION_BUS_ADDRESS": runtime["DBUS_SESSION_BUS_ADDRESS"]}
}

func inspectNamedArtifacts(dir *os.File, entries []string) (map[string]unix.Stat_t, error) {
	var artifacts = make(map[string]unix.Stat_t)
	for _, entry := range entries {
		var kind uint32
		switch entry {
		case "record", "record.tmp", "status", "status.tmp":
			kind = unix.S_IFREG
		case "control":
			kind = unix.S_IFSOCK
		default:
			return nil, errors.New("unknown registry artifact")
		}
		var st unix.Stat_t
		if err := unix.Fstatat(int(dir.Fd()), entry, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, err
		}
		if st.Mode != kind|0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
			return nil, errors.New("unsafe registry artifact")
		}
		artifacts[entry] = st
	}
	return artifacts, nil
}

func authenticateNamedRecovery(dir *os.File, name string, artifacts map[string]unix.Stat_t) error {
	// A published record must authenticate. An interrupted publication may
	// leave only a partial temporary record or a socket.
	if _, exists := artifacts["record"]; exists {
		rec, err := namedRead(dir)
		if err != nil {
			return fmt.Errorf("cannot authenticate stale record: %w", err)
		}
		if rec.Name != name || rec.PID <= 0 {
			return errors.New("session identity mismatch")
		}
		if start, parseErr := strconv.ParseUint(rec.Start, 10, 64); parseErr != nil || start == 0 {
			return errors.New("invalid owner start identity")
		}
		terminalStatus := false
		if _, hasStatus := artifacts["status"]; hasStatus {
			if _, hasControl := artifacts["control"]; !hasControl {
				_, statusErr := workflowReadTerminalStatus(dir, name)
				terminalStatus = statusErr == nil
			}
		}
		start, err := namedProcStart(rec.PID)
		if err == nil {
			if start == rec.Start {
				if !terminalStatus {
					return errors.New("session owner still live")
				}
			}
			// A different valid start time proves the recorded owner is gone.
			// Recovery only unlinks validated registry artifacts, not processes.
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("owner identity uncertain: %w", err)
		}
	}

	return nil
}

func recheckNamedArtifacts(root, dir *os.File, name string, artifacts map[string]unix.Stat_t) error {
	var own, current unix.Stat_t
	if err := unix.Fstat(int(dir.Fd()), &own); err != nil {
		return err
	}
	if err := unix.Fstatat(int(root.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if own.Ino != current.Ino || own.Dev != current.Dev || own.Mode != current.Mode || own.Uid != current.Uid {
		return errors.New("reservation changed")
	}
	for entry, previous := range artifacts {
		var st unix.Stat_t
		if err := unix.Fstatat(int(dir.Fd()), entry, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if !sameNamedArtifact(st, previous) {
			return errors.New("registry artifact changed")
		}
	}

	return nil
}

func authenticateNamedPeer(c *net.UnixConn, rec Record) error {
	peer, err := namedPeer(c)
	if err != nil || peer.Uid != uint32(os.Geteuid()) || int(peer.Pid) != rec.PID {
		return errors.New("session owner authentication failed")
	}
	start, err := namedProcStart(rec.PID)
	if err != nil || start != rec.Start {
		return errors.New("session owner identity changed")
	}
	return nil
}

func exchangeNamedRequest(ctx context.Context, c *net.UnixConn, rec Record, name, operation string) (Reply, error) {
	var reply Reply
	if ctxErr := ctx.Err(); ctxErr != nil {
		return reply, ctxErr
	}
	if encodeErr := json.NewEncoder(c).Encode(namedRequest{rec.ID, operation}); encodeErr != nil {
		return reply, encodeErr
	}
	if decodeErr := json.NewDecoder(io.LimitReader(c, 8192)).Decode(&reply); decodeErr != nil {
		return reply, decodeErr
	}
	if reply.ID != rec.ID || reply.Name != name {
		return Reply{}, errors.New("session reply identity mismatch")
	}
	if operation == "stop" && reply.State != "stopped" {
		return Reply{}, errors.New("session stop not confirmed")
	}
	return reply, ctx.Err()
}

func sameNamedArtifact(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Nlink == b.Nlink && a.Size == b.Size && a.Ctim == b.Ctim && a.Mtim == b.Mtim
}

func namedRequestDeadline(ctx context.Context, operation string) time.Time {
	deadline := time.Now().Add(2 * time.Second)
	if operation == "stop" {
		deadline = time.Now().Add(30 * time.Second)
	}
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return deadline
}

// List returns named-session status in the existing registry enumeration order.
func List(ctx context.Context, root *os.File) ([]Reply, error) {
	names, err := root.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	replies := []Reply{}
	for _, name := range names {
		if !sessionNamePattern.MatchString(name) {
			continue
		}
		reply, callErr := Call(ctx, root, name, "inspect")
		if callErr != nil {
			state := "unavailable"
			if errors.Is(callErr, errNamedStale) {
				state = "stale"
			}
			reply = Reply{Name: name, State: state}
		}
		replies = append(replies, reply)
	}

	return replies, nil
}

// ValidName checks the existing registry name grammar.
func ValidName(name string) bool { return sessionNamePattern.MatchString(name) }

// CloseListener releases the listening endpoint before the owner is retired.
func (o *Owner) CloseListener() error { return o.listener.Close() }

// Record returns a snapshot of the immutable owner identity.
func (o *Owner) Record() Record { return o.record }

// Directory returns the opened reservation directory for FD-relative operations.
func (o *Owner) Directory() *os.File { return o.dir }
