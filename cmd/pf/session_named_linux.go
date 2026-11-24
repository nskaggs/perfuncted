//go:build linux

package main

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

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var sessionNamePattern = regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$")

type namedRecord struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

type namedReply struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	State       string            `json:"state"`
	Environment map[string]string `json:"environment,omitempty"`
}

type namedRequest struct {
	ID        string
	Operation string
}

type namedOwner struct {
	root     *os.File
	dir      *os.File
	record   namedRecord
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

func namedOpenDir(parent int, name string) (*os.File, error) {
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

func namedRegistry() (*os.File, error) {
	// A fixed per-UID location under sticky /tmp is independent of the
	// caller's XDG_RUNTIME_DIR, including after attaching to another session.
	path := fmt.Sprintf("/tmp/pf-sessions-%d", os.Geteuid())
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	return namedOpenDir(unix.AT_FDCWD, path)
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

func namedReserve(name string) (*namedOwner, error) {
	if !sessionNamePattern.MatchString(name) {
		return nil, errors.New("invalid session name (1-32 letters, digits, underscores or hyphens)")
	}
	root, err := namedRegistry()
	if err != nil {
		return nil, err
	}
	return namedReserveIn(root, name)
}

// namedReserveIn takes ownership of root, even on failure.
func namedReserveIn(root *os.File, name string) (*namedOwner, error) {
	// Serialize creation through acquisition of the reservation lock.
	if err := unix.Flock(int(root.Fd()), unix.LOCK_EX); err != nil {
		root.Close()
		return nil, err
	}
	if err := unix.Mkdirat(int(root.Fd()), name, 0700); err != nil {
		root.Close()
		return nil, fmt.Errorf("reserve session name: %w", err)
	}
	dir, err := namedOpenDir(int(root.Fd()), name)
	if err != nil {
		root.Close()
		return nil, err
	}
	if err := unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		dir.Close()
		root.Close()
		return nil, err
	}
	if err = unix.Flock(int(root.Fd()), unix.LOCK_UN); err != nil {
		dir.Close()
		root.Close()
		return nil, err
	}
	o := &namedOwner{root: root, dir: dir}
	o.record.Name = name
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		o.close()
		return nil, err
	}
	o.record.ID = hex.EncodeToString(id[:])
	o.record.PID = os.Getpid()
	o.record.Start, err = namedProcStart(o.record.PID)
	if err != nil {
		o.close()
		return nil, err
	}
	return o, nil
}

// Bind through the opened directory, not a mutable registry pathname.
func namedFDPath(dir *os.File, name string) string {
	return fmt.Sprintf("/proc/self/fd/%d/%s", dir.Fd(), name)
}

func (o *namedOwner) close() {
	if o.listener != nil {
		o.listener.Close()
	}
	unix.Unlinkat(int(o.dir.Fd()), "control", 0)
	unix.Unlinkat(int(o.dir.Fd()), "record", 0)
	var owned, current unix.Stat_t
	if unix.Fstat(int(o.dir.Fd()), &owned) == nil &&
		unix.Fstatat(int(o.root.Fd()), o.record.Name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil &&
		owned.Ino == current.Ino && owned.Dev == current.Dev {
		unix.Unlinkat(int(o.root.Fd()), o.record.Name, unix.AT_REMOVEDIR)
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

func (o *namedOwner) publish() error {
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
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	// Publish metadata only after binding. An exclusive regular file in the
	// private reservation cannot overwrite a symlink or preexisting inode.
	fd, err := unix.Openat(int(o.dir.Fd()), "record.tmp", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "record")
	defer unix.Unlinkat(int(o.dir.Fd()), "record.tmp", 0)
	err = json.NewEncoder(f).Encode(o.record)
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return unix.Renameat2(int(o.dir.Fd()), "record.tmp", int(o.dir.Fd()), "record", unix.RENAME_NOREPLACE)
}

func namedRead(dir *os.File) (namedRecord, error) {
	var rec namedRecord
	fd, err := unix.Openat(int(dir.Fd()), "record", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return rec, err
	}
	f := os.NewFile(uintptr(fd), "record")
	defer f.Close()
	if err = namedCheck(fd, unix.S_IFREG, 0600, true); err != nil {
		return rec, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return rec, err
	}
	if len(data) > 4096 {
		return rec, errors.New("oversized session record")
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, err
	}
	id, err := hex.DecodeString(rec.ID)
	if err != nil || len(id) != 16 || !sessionNamePattern.MatchString(rec.Name) {
		return rec, errors.New("invalid session record")
	}
	return rec, nil
}

func (o *namedOwner) serve(ctx context.Context, env map[string]string, stop context.CancelFunc, finished <-chan error) {
	defer stop()
	for {
		c, err := o.listener.AcceptUnix()
		if err != nil {
			return
		}
		halt := o.handle(c, ctx, env, stop, finished)
		c.Close()
		if halt {
			return
		}
	}
}

func (o *namedOwner) handle(c *net.UnixConn, ctx context.Context, env map[string]string, stop context.CancelFunc, finished <-chan error) bool {
	c.SetDeadline(time.Now().Add(time.Second))
	peer, err := namedPeer(c)
	if err != nil || peer.Uid != uint32(os.Geteuid()) || ctx.Err() != nil {
		return false
	}
	var req namedRequest
	if json.NewDecoder(io.LimitReader(c, 4096)).Decode(&req) != nil || req.ID != o.record.ID {
		return false
	}
	reply := namedReply{ID: o.record.ID, Name: o.record.Name, State: "ready"}
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
func namedRecover(root *os.File, name string) error {
	if !sessionNamePattern.MatchString(name) {
		return errors.New("invalid session name")
	}
	if err := namedCheck(int(root.Fd()), unix.S_IFDIR, 0700, false); err != nil {
		return err
	}
	if err := unix.Flock(int(root.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("registry busy: %w", err)
	}
	defer unix.Flock(int(root.Fd()), unix.LOCK_UN)
	dir, err := namedOpenDir(int(root.Fd()), name)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := unix.Flock(int(dir.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("session reservation is active: %w", err)
	}
	entries, err := dir.Readdirnames(-1)
	if err != nil {
		return err
	}
	artifacts := make(map[string]unix.Stat_t)
	for _, entry := range entries {
		var kind uint32
		switch entry {
		case "record", "record.tmp", "status", "status.tmp":
			kind = unix.S_IFREG
		case "control":
			kind = unix.S_IFSOCK
		default:
			return errors.New("unknown registry artifact")
		}
		var st unix.Stat_t
		if err := unix.Fstatat(int(dir.Fd()), entry, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if st.Mode != kind|0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
			return errors.New("unsafe registry artifact")
		}
		artifacts[entry] = st
	}
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
		if start, err := strconv.ParseUint(rec.Start, 10, 64); err != nil || start == 0 {
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
		if st.Dev != previous.Dev || st.Ino != previous.Ino || st.Mode != previous.Mode || st.Uid != previous.Uid || st.Nlink != previous.Nlink || st.Size != previous.Size || st.Ctim != previous.Ctim || st.Mtim != previous.Mtim {
			return errors.New("registry artifact changed")
		}
	}
	for _, entry := range entries {
		if err := unix.Unlinkat(int(dir.Fd()), entry, 0); err != nil {
			return err
		}
	}
	return unix.Unlinkat(int(root.Fd()), name, unix.AT_REMOVEDIR)
}

func namedCall(ctx context.Context, root *os.File, name, operation string) (namedReply, error) {
	var reply namedReply
	dir, err := namedOpenDir(int(root.Fd()), name)
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
	if err = unix.Fstatat(int(dir.Fd()), "control", &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return reply, err
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
	c := conn.(*net.UnixConn)
	deadline := time.Now().Add(2 * time.Second)
	if operation == "stop" {
		deadline = time.Now().Add(30 * time.Second)
	}
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	cancelWatch := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer cancelWatch()
	peer, err := namedPeer(c)
	if err != nil || peer.Uid != uint32(os.Geteuid()) || int(peer.Pid) != rec.PID {
		return reply, errors.New("session owner authentication failed")
	}
	start, err = namedProcStart(rec.PID)
	if err != nil || start != rec.Start {
		return reply, errors.New("session owner identity changed")
	}
	if err = ctx.Err(); err != nil {
		return reply, err
	}
	if err = json.NewEncoder(c).Encode(namedRequest{rec.ID, operation}); err != nil {
		return reply, err
	}
	if err = json.NewDecoder(io.LimitReader(c, 8192)).Decode(&reply); err != nil {
		return reply, err
	}
	if reply.ID != rec.ID || reply.Name != name {
		return namedReply{}, errors.New("session reply identity mismatch")
	}
	if operation == "stop" && reply.State != "stopped" {
		return namedReply{}, errors.New("session stop not confirmed")
	}
	return reply, ctx.Err()
}

func namedSafeEnv(runtime map[string]string) map[string]string {
	return map[string]string{"XDG_RUNTIME_DIR": runtime["XDG_RUNTIME_DIR"], "WAYLAND_DISPLAY": runtime["WAYLAND_DISPLAY"], "DBUS_SESSION_BUS_ADDRESS": runtime["DBUS_SESSION_BUS_ADDRESS"]}
}

func namedShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func namedCommands() []*cobra.Command {
	return namedCommandsWithRegistry(namedRegistry)
}

func namedCommandsWithRegistry(registry func() (*os.File, error)) []*cobra.Command {
	var commands []*cobra.Command
	for _, op := range []string{"list", "inspect", "attach", "stop", "recover"} {
		operation := op
		var output string
		var printEnv bool

		args := cobra.ExactArgs(1)
		use := operation + " NAME"
		if operation == "list" {
			args = cobra.NoArgs
			use = operation
		}
		cmd := &cobra.Command{Use: use, Short: "Manage named foreground headless sessions: " + operation, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := parseOutputMode(output)
			if err != nil {
				return err
			}
			root, err := registry()
			if err != nil {
				return err
			}
			defer root.Close()
			if operation == "list" {
				names, err := root.Readdirnames(-1)
				if err != nil {
					return err
				}
				replies := []namedReply{}
				for _, name := range names {
					if !sessionNamePattern.MatchString(name) {
						continue
					}
					reply, err := namedCall(cmd.Context(), root, name, "inspect")
					if err != nil {
						state := "unavailable"
						if errors.Is(err, errNamedStale) {
							state = "stale"
						}
						reply = namedReply{Name: name, State: state}
					}
					replies = append(replies, reply)
				}
				if mode == outputModeJSON {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(replies)
				}
				for _, r := range replies {
					if err = writeCLIOutput(cmd.OutOrStdout(), "%s %s %s\n", r.Name, r.ID, r.State); err != nil {
						return err
					}
				}
				return nil
			}
			if !sessionNamePattern.MatchString(args[0]) {
				return errors.New("invalid session name")
			}
			if printEnv && mode == outputModeJSON {
				return errors.New("--print-env conflicts with JSON output")
			}
			if operation == "recover" {
				if err := namedRecover(root, args[0]); err != nil {
					return err
				}
				if mode == outputModeJSON {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(namedReply{Name: args[0], State: "recovered"})
				}
				return writeCLIOutput(cmd.OutOrStdout(), "%s recovered\n", args[0])
			}
			reply, err := namedCall(cmd.Context(), root, args[0], operation)
			if err != nil {
				return err
			}
			if mode == outputModeJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(reply)
			}
			if operation == "attach" {
				for _, key := range []string{"XDG_RUNTIME_DIR", "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS"} {
					if err = writeCLIOutput(cmd.OutOrStdout(), "export %s=%s\n", key, namedShellQuote(reply.Environment[key])); err != nil {
						return err
					}
				}
				return nil
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%s %s %s\n", reply.Name, reply.ID, reply.State)
		}}
		if operation == "recover" {
			cmd.Long = "Remove only known registry artifacts from a stale named reservation after obtaining its exclusive owner lock. Unpublished reservations may contain partial temporary metadata; published records must have a verified absent owner or a reused PID with a different valid start time. Unsafe or uncertain identities are refused. No process or desktop runtime is removed."
		}
		cmd.Flags().StringVar(&output, "output", "plain", "plain|json")

		if operation == "attach" {
			cmd.Flags().BoolVar(&printEnv, "print-env", false, "emit shell exports (also the plain-output default)")
		}
		commands = append(commands, cmd)
	}
	return commands
}
