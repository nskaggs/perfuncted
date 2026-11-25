package runtime

import (
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadSessionSubdirMounts reads mount points within an explicitly scoped runtime.
func ReadSessionSubdirMounts(dir string) ([]string, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return ParseSessionSubdirMounts(dir, data), nil
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

// ParseSessionSubdirMounts decodes kernel mount paths and selects only the runtime tree.
func ParseSessionSubdirMounts(dir string, data []byte) []string {
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

// ReadSessionPIDMetadata treats runtime PID files as untrusted: a replaced
// FIFO must not block cleanup, and links/devices must not be read as ownership
// claims. A PID is short, so never read an unbounded file into memory.
func ReadSessionPIDMetadata(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var stat unix.Stat_t
	if statErr := unix.Fstat(fd, &stat); statErr != nil {
		return nil, statErr
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
		return nil, fmt.Errorf("session: PID metadata %q must be a single-link regular file", path)
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&0022 != 0 {
		return nil, fmt.Errorf("session: PID metadata %q must be owned by the current user and not writable by others", path)
	}
	const maxPIDBytes = 64
	bytes, err := io.ReadAll(io.LimitReader(file, maxPIDBytes+1))
	if err != nil {
		return nil, err
	}
	if len(bytes) > maxPIDBytes {
		return nil, fmt.Errorf("session: PID metadata %q is oversized", path)
	}
	return bytes, nil
}

// ReadPIDFile reads and validates a positive PID from trusted runtime metadata.
func ReadPIDFile(path string) (int, error) {
	data, err := ReadSessionPIDMetadata(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid in %s", path)
	}
	return pid, nil
}

// WriteManagedSessionFile replaces only the directory entry inside the private
// runtime. os.WriteFile would follow a preplaced symlink or overwrite the inode
// behind a hard link, potentially corrupting a file outside this runtime.
// Rename preserves the existing contract that Sway configs and child PID files
// can be updated while keeping partial writes invisible to readers.
func WriteManagedSessionFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".perfuncted-managed-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
