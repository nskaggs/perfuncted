//go:build linux

package input

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nskaggs/perfuncted/internal/env"
	"golang.org/x/sys/unix"
)

const portalRestoreTokenFile = "remote-desktop-restore-token"
const portalRestoreLockFile = "remote-desktop-restore.lock"

type portalTokenLock struct {
	dirfd int
	fd    int
}

func lockPortalTokenStore(rt env.Runtime) (*portalTokenLock, error) {
	stateHome := strings.TrimSpace(rt.Get("XDG_STATE_HOME"))
	if stateHome == "" {
		home := strings.TrimSpace(rt.Get("HOME"))
		if home == "" {
			return nil, errors.New("XDG_STATE_HOME or HOME is required")
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(stateHome) {
		return nil, errors.New("state directory must be absolute")
	}
	dirfd, err := openPrivatePortalDirectory(filepath.Join(stateHome, "perfuncted"))
	if err != nil {
		return nil, err
	}
	lockfd, err := unix.Openat(dirfd, portalRestoreLockFile, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		_ = unix.Close(dirfd)
		return nil, err
	}
	if err := validatePortalPrivateFile(lockfd); err != nil {
		_ = unix.Close(lockfd)
		_ = unix.Close(dirfd)
		return nil, err
	}
	if err := unix.Flock(lockfd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(lockfd)
		_ = unix.Close(dirfd)
		return nil, fmt.Errorf("restore-token store is already in use: %w", err)
	}
	return &portalTokenLock{dirfd: dirfd, fd: lockfd}, nil
}

func openPrivatePortalDirectory(path string) (int, error) { //nolint:gocyclo // every path component is opened and checked through its directory descriptor.
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return -1, errors.New("state directory must be absolute")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			_ = unix.Close(fd)
			return -1, errors.New("state path contains an invalid component")
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) {
			if mkdirErr := unix.Mkdirat(fd, part, 0700); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, mkdirErr
			}
			next, openErr = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if openErr != nil {
			_ = unix.Close(fd)
			return -1, openErr
		}
		var stat unix.Stat_t
		if err := unix.Fstat(next, &stat); err != nil {
			_ = unix.Close(next)
			_ = unix.Close(fd)
			return -1, err
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
			_ = unix.Close(next)
			_ = unix.Close(fd)
			return -1, errors.New("state path component is not a directory")
		}
		permissions := stat.Mode & 0777
		if index == len(parts)-1 {
			if stat.Uid != uint32(unix.Geteuid()) || permissions&0077 != 0 {
				_ = unix.Close(next)
				_ = unix.Close(fd)
				return -1, errors.New("application state directory must be owned by this user and private")
			}
		} else if permissions&0022 != 0 && (stat.Uid != 0 || stat.Mode&unix.S_ISVTX == 0) {
			_ = unix.Close(next)
			_ = unix.Close(fd)
			return -1, errors.New("state path contains a group or world writable directory")
		}
		_ = unix.Close(fd)
		fd = next
	}
	return fd, nil
}

func validatePortalPrivateFile(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(unix.Geteuid()) || stat.Nlink != 1 || stat.Mode&0777 != 0600 {
		return errors.New("restore-token file must be a private regular file owned by this user")
	}
	return nil
}

func (l *portalTokenLock) consume() (string, error) { //nolint:gocyclo // token consumption validates ownership, size, contents, and single-use unlink atomically.
	if l == nil || l.fd < 0 {
		return "", errors.New("restore-token store is closed")
	}
	fd, err := unix.Openat(l.dirfd, portalRestoreTokenFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err := validatePortalPrivateFile(fd); err != nil {
		_ = unix.Close(fd)
		return "", err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return "", err
	}
	if stat.Size < 1 || stat.Size > 4097 {
		_ = unix.Close(fd)
		return "", errors.New("restore-token file has an invalid size")
	}
	data := make([]byte, int(stat.Size))
	read := 0
	for read < len(data) {
		n, readErr := unix.Read(fd, data[read:])
		if readErr != nil {
			_ = unix.Close(fd)
			return "", readErr
		}
		if n == 0 {
			break
		}
		read += n
	}
	if err := unix.Close(fd); err != nil {
		return "", err
	}
	if err := unix.Unlinkat(l.dirfd, portalRestoreTokenFile, 0); err != nil {
		return "", err
	}
	data = data[:read]
	data = []byte(strings.TrimSuffix(string(data), "\n"))
	if len(data) == 0 || strings.ContainsAny(string(data), "\x00\r\n") {
		return "", errors.New("restore-token file is malformed")
	}
	return string(data), nil
}

func (l *portalTokenLock) store(token string) error { //nolint:gocyclo // private atomic storage checks the temporary file and directory durability steps.
	if l == nil || l.fd < 0 {
		return errors.New("restore-token store is closed")
	}
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, "\x00\r\n") {
		return errors.New("portal returned a malformed restore token")
	}
	var random [12]byte
	if _, err := unix.Getrandom(random[:], 0); err != nil {
		return err
	}
	temporary := ".restore-token-" + fmt.Sprintf("%x", random[:])
	fd, err := unix.Openat(l.dirfd, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = unix.Unlinkat(l.dirfd, temporary, 0)
		}
	}()
	if err := validatePortalPrivateFile(fd); err != nil {
		_ = unix.Close(fd)
		return err
	}
	data := []byte(token + "\n")
	for len(data) > 0 {
		n, writeErr := unix.Write(fd, data)
		if writeErr != nil {
			_ = unix.Close(fd)
			return writeErr
		}
		if n == 0 {
			_ = unix.Close(fd)
			return errors.New("zero-length restore-token write")
		}
		data = data[n:]
	}
	if err := unix.Fsync(fd); err != nil {
		_ = unix.Close(fd)
		return err
	}
	if err := unix.Close(fd); err != nil {
		return err
	}
	if err := unix.Renameat(l.dirfd, temporary, l.dirfd, portalRestoreTokenFile); err != nil {
		return err
	}
	removeTemporary = false
	return unix.Fsync(l.dirfd)
}

func (l *portalTokenLock) Close() error {
	if l == nil {
		return nil
	}
	var err error
	if l.fd >= 0 {
		err = errors.Join(err, unix.Flock(l.fd, unix.LOCK_UN), unix.Close(l.fd))
		l.fd = -1
	}
	if l.dirfd >= 0 {
		err = errors.Join(err, unix.Close(l.dirfd))
		l.dirfd = -1
	}
	return err
}
