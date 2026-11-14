package window

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A canceled explicit SWAYSOCK probe cannot silently fall back to a
// different desktop's runtime sockets. The caller must receive cancellation.
func TestSwayExplicitProbeCancellationCannotFallBackToRuntimeDirectory(t *testing.T) {
	original := swayDialContext
	t.Cleanup(func() { swayDialContext = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dials := 0
	swayDialContext = func(_ context.Context, network, address string) (net.Conn, error) {
		dials++
		if network != "unix" || address != "/private/selected.sock" {
			t.Fatalf("unexpected fallback dial: %s %s", network, address)
		}
		cancel()
		return nil, errors.New("synthetic transport failure on selected socket")
	}
	rt := env.FromEnviron([]string{"SWAYSOCK=/private/selected.sock", "XDG_RUNTIME_DIR=/unexpected-fallback"})
	m, err := NewSwayManagerRuntimeContext(ctx, rt)
	if m != nil || !errors.Is(err, context.Canceled) || dials != 1 {
		t.Fatalf("canceled selected socket triggered fallback: manager=%v err=%v dials=%d", m, err, dials)
	}
}

// ActiveTitle cannot interpret syntactically valid but structurally invalid
// JSON as absence of focus; that would publish a false desktop observation.
func TestSwayActiveTitleRejectsUntrustedTreeRoot(t *testing.T) {
	for _, tc := range []struct{ name, tree string }{
		{"null", "null"},
		{"missing identity", `{"nodes":[{"id":44,"type":"con","name":"private","focused":true}]}`},
		{"wrong root kind", `{"id":1,"type":"con","nodes":[{"id":44,"type":"con","name":"private","focused":true}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, tc.tree, `[{"success":true}]`)
			title, err := m.ActiveTitle(context.Background())
			if title != "" || err == nil || !strings.Contains(err.Error(), "invalid tree root") {
				t.Errorf("untrusted root supplied active title: title=%q err=%v", title, err)
			}
			commands, serverErr := finish()
			if serverErr != nil || len(commands) != 1 || commands[0] != "GET_TREE" {
				t.Fatalf("unexpected synthetic IPC: commands=%v err=%v", commands, serverErr)
			}
		})
	}
}
