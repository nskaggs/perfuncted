package accessibility

import (
	"fmt"
	"strings"
	"testing"
)

// This independent historical-format oracle protects the cache identity
// when its allocation strategy changes.
func TestSnapshotKeyEncodingMatchesCacheContract(t *testing.T) {
	roots := []NodeID{
		{BusName: "org.example.App", ObjectPath: "/app", Generation: 7, Incarnation: 2},
		{BusName: "", ObjectPath: "", Generation: ^uint64(0)},
	}
	options := []SnapshotOptions{
		{},
		{MaxDepth: 20, MaxNodes: 4000, MaxTextBytes: 4096, MaxTotalBytes: 8 << 20},
		{MaxDepth: -9, MaxNodes: -2, MaxTextBytes: -1, MaxTotalBytes: -3,
			VisibleOnly: true, AllowSensitive: true, AllowDesktopRoot: true,
			SkipRoles: []string{"button", "menu,item", "écran"}},
	}
	for _, root := range roots {
		for _, opts := range options {
			want := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d\x00%d\x00%t\x00%t\x00%t\x00%s",
				root.BusName, root.ObjectPath, root.Generation, effectiveIncarnation(root),
				opts.MaxDepth, opts.MaxNodes, opts.MaxTextBytes, opts.MaxTotalBytes,
				opts.VisibleOnly, opts.AllowSensitive, opts.AllowDesktopRoot, strings.Join(opts.SkipRoles, ","))
			if got := snapshotKey(root, opts); got != want {
				t.Fatalf("snapshotKey(%+v, %+v) = %q; want %q", root, opts, got, want)
			}
		}
	}
}
