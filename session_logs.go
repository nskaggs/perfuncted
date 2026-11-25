package perfuncted

import (
	"time"

	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
)

// CleanupSessionLogs removes only expired, uniquely-created session log
// directories from root. It never scans or removes arbitrary files in the
// configured parent directory.
func CleanupSessionLogs(root string, maxAge time.Duration) {
	sessionruntime.CleanupSessionLogs(root, maxAge)
}
