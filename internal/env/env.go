package env

import (
	"strings"
)

// Environ builds a complete environment variable slice by overlaying session
// variables on the current process environment. This mirrors the previous
// session.Environ implementation but centralizes it for reuse.
func Environ(xdgRuntimeDir, waylandDisplay, dbusAddr string) []string {
	return Current().WithSession(xdgRuntimeDir, waylandDisplay, dbusAddr).EnvList()
}

// Merge overlays the provided overlay entries on top of the base environment.
// Overlays are strings of the form KEY=VALUE. If an overlay key exists in the
// base, its value is replaced. The returned slice preserves the order of base
// entries (except those overridden) followed by overlays in the order provided.
func Merge(base []string, overlays ...string) []string {
	if len(overlays) == 0 {
		// Make a shallow copy of base to avoid modifying caller's slice.
		out := make([]string, len(base))
		copy(out, base)
		return out
	}
	// Parse overlays into map and order.
	overMap := make(map[string]string, len(overlays))
	overKeys := make([]string, 0, len(overlays))
	for _, kv := range overlays {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			// Treat whole string as key with empty value.
			if _, ok := overMap[kv]; !ok {
				overKeys = append(overKeys, kv)
			}
			overMap[kv] = ""
			continue
		}
		k := kv[:i]
		v := kv[i+1:]
		if _, ok := overMap[k]; !ok {
			overKeys = append(overKeys, k)
		}
		overMap[k] = v
	}

	out := make([]string, 0, len(base)+len(overlays))
	seen := make(map[string]bool, len(base)+len(overlays))
	for _, kv := range base {
		i := strings.Index(kv, "=")
		if i < 0 {
			// keep malformed entries
			out = append(out, kv)
			continue
		}
		k := kv[:i]
		if _, overr := overMap[k]; overr {
			// skip base key — it will be added from overlays
			continue
		}
		out = append(out, kv)
		seen[k] = true
	}
	// append overlays in order
	for _, k := range overKeys {
		v := overMap[k]
		out = append(out, k+"="+v)
		seen[k] = true
	}
	return out
}
