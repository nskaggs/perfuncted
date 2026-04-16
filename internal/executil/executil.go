package executil

import (
	"os/exec"
)

// LookPath is a variable indirection for exec.LookPath so tests can override
// it for hermetic behavior.
var LookPath = exec.LookPath

// CommandContext is a variable indirection for exec.CommandContext so tests
// can override command execution behavior if needed.
var CommandContext = exec.CommandContext
