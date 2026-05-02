package executil

import (
	"os/exec"
	"slices"
	"strings"
)

// LookPath is a variable indirection for exec.LookPath so tests can override
// it for hermetic behavior.
var LookPath = exec.LookPath

// CommandContext is a variable indirection for exec.CommandContext so tests
// can override command execution behavior if needed.
var CommandContext = exec.CommandContext

// MergeEnv merges extra entries with base environment values, giving precedence
// to entries from extra. Result is a deterministic, sorted slice suitable for
// assigning to exec.Cmd.Env.
func MergeEnv(extra, base []string) []string {
	m := make(map[string]string, len(base)+len(extra))
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		} else {
			m[kv] = ""
		}
	}
	for _, kv := range extra {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		} else {
			m[kv] = ""
		}
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out
}
