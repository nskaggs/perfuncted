//go:build desktopbench && linux

package perfuncted

import (
	"context"
	"image"
	"os/exec"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

const desktopBenchmarkTimeout = 2 * time.Minute

func desktopBenchmarkOptions(logDir string, accessibilityOptional bool) []Option {
	options := []Option{
		WithHeadless(SessionConfig{Resolution: image.Pt(1024, 768), LogDir: logDir}),
		Require(CapabilityScreen, CapabilityInput, CapabilityWindows),
	}
	if accessibilityOptional {
		options = append(options, Optional(CapabilityAccessibility))
	}
	return options
}

func openDesktopBenchmarkSession(b *testing.B, logDir string, accessibilityOptional bool) *Session {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
	defer cancel()
	session, err := Open(ctx, desktopBenchmarkOptions(logDir, accessibilityOptional)...)
	if err != nil {
		b.Fatalf("open real headless benchmark session: %v", err)
	}
	b.Cleanup(func() {
		if err := session.Close(); err != nil {
			b.Errorf("close benchmark session: %v", err)
		}
	})
	b.Logf("target=%s screen=%T input=%T windows=%T timeouts=%+v", session.Target().Kind(), session.Screen, session.Input, session.Windows, session.Timeouts())
	return session
}

// BenchmarkDesktopOpenClose measures managed desktop startup and shutdown,
// including the real D-Bus and compositor process boundary.
func BenchmarkDesktopOpenClose(b *testing.B) {
	// Use a private root so startup measurements include real log-directory
	// creation without charging each iteration for unrelated host history in
	// the process-wide retention directory.
	logDir := b.TempDir()
	probe := openDesktopBenchmarkSession(b, logDir, false)
	if err := probe.Close(); err != nil {
		b.Fatalf("close benchmark label probe: %v", err)
	}
	b.ResetTimer()
	for b.Loop() {
		ctx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
		session, err := Open(ctx, desktopBenchmarkOptions(logDir, false)...)
		cancel()
		if err != nil {
			b.Fatalf("open real headless benchmark session: %v", err)
		}
		if err := session.Close(); err != nil {
			b.Fatalf("close benchmark session: %v", err)
		}
	}
}

// BenchmarkDesktopRegionHash measures one real compositor-backed region hash.
func BenchmarkDesktopRegionHash(b *testing.B) {
	session := openDesktopBenchmarkSession(b, b.TempDir(), false)
	ctx := context.Background()
	region := image.Rect(0, 0, 256, 256)
	for b.Loop() {
		if _, err := session.Screen.GrabRegionHash(ctx, region); err != nil {
			b.Fatalf("region hash: %v", err)
		}
	}
}

func launchDesktopBenchmarkWindow(b *testing.B, session *Session) (*Application, *Window) {
	b.Helper()
	commands := []struct {
		name  string
		args  []string
		match string
	}{
		{name: "kwrite", args: []string{"--standalone"}, match: "kwrite"},
		{name: "gnome-text-editor", match: "text"},
		{name: "featherpad", match: "featherpad"},
	}
	for _, candidate := range commands {
		if _, err := exec.LookPath(candidate.name); err != nil {
			continue
		}
		app, err := session.Launch(context.Background(), Command{
			Name:   candidate.name,
			Args:   candidate.args,
			Stdout: nil,
			Stderr: nil,
		})
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
		window, waitErr := app.WaitForWindow(ctx, WindowMatch{TitleContains: candidate.match})
		cancel()
		if waitErr == nil {
			return app, window
		}
		_ = app.Kill()
	}
	b.Skip("desktop benchmark requires a supported GUI editor")
	return nil, nil
}

// BenchmarkDesktopInputClickType measures real pointer and keyboard actions.
func BenchmarkDesktopInputClickType(b *testing.B) {
	session := openDesktopBenchmarkSession(b, b.TempDir(), false)
	_, window := launchDesktopBenchmarkWindow(b, session)
	ctx := context.Background()
	if err := window.Activate(ctx); err != nil {
		b.Fatalf("activate benchmark window: %v", err)
	}
	for b.Loop() {
		if err := session.Input.MouseClick(ctx, 8, 8, 1); err != nil {
			b.Fatalf("click: %v", err)
		}
		if err := session.Input.TypeLiteral(ctx, "x"); err != nil {
			b.Fatalf("type: %v", err)
		}
	}
}

// BenchmarkDesktopWindowListActivate measures window synchronization and
// activation against a real application window.
func BenchmarkDesktopWindowListActivate(b *testing.B) {
	session := openDesktopBenchmarkSession(b, b.TempDir(), false)
	_, window := launchDesktopBenchmarkWindow(b, session)
	ctx := context.Background()
	for b.Loop() {
		windows, err := session.Windows.List(ctx, WindowMatch{})
		if err != nil {
			b.Fatalf("list windows: %v", err)
		}
		if len(windows) == 0 {
			b.Fatal("window list unexpectedly empty")
		}
		if err := window.Activate(ctx); err != nil {
			b.Fatalf("activate window: %v", err)
		}
	}
}

func openAccessibilityBenchmarkRoot(b *testing.B) (*Session, accessibility.NodeID) {
	b.Helper()
	session := openDesktopBenchmarkSession(b, b.TempDir(), true)
	status := session.Capability(CapabilityAccessibility)
	if !status.Available {
		b.Skipf("accessibility capability unavailable: %v", status.Failure)
	}
	// A managed session without an application has no AT-SPI application root;
	// launch the same real editor used by the input/window benchmarks before
	// measuring the accessibility boundary.
	launchDesktopBenchmarkWindow(b, session)
	ctx, cancel := context.WithTimeout(context.Background(), session.Timeouts().Short)
	defer cancel()
	apps, err := session.Accessibility.Applications(ctx)
	if err != nil {
		b.Skipf("accessibility applications unavailable: %v", err)
	}
	if len(apps) == 0 {
		b.Skip("accessibility backend has no application root")
	}
	return session, apps[0].ID
}

// BenchmarkDesktopAccessibilitySnapshot measures one bounded AT-SPI snapshot.
func BenchmarkDesktopAccessibilitySnapshot(b *testing.B) {
	session, root := openAccessibilityBenchmarkRoot(b)
	options := accessibility.SnapshotOptions{MaxDepth: 8, MaxNodes: 256, MaxTextBytes: 512}
	ctx := context.Background()
	for b.Loop() {
		if _, err := session.Accessibility.Snapshot(ctx, root, options); err != nil {
			b.Fatalf("accessibility snapshot: %v", err)
		}
	}
}

// BenchmarkDesktopAccessibilityAction measures one real typed AT-SPI action
// when the selected application exposes an actionable node.
func BenchmarkDesktopAccessibilityAction(b *testing.B) {
	session, root := openAccessibilityBenchmarkRoot(b)
	ctx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
	snapshot, err := session.Accessibility.Snapshot(ctx, root, accessibility.SnapshotOptions{MaxDepth: 8, MaxNodes: 256, MaxTextBytes: 512})
	cancel()
	if err != nil {
		b.Skipf("accessibility snapshot unavailable: %v", err)
	}
	var actionNode accessibility.Node
	for _, node := range snapshot.Nodes {
		if len(node.Actions) > 0 {
			actionNode = node
			break
		}
	}
	if actionNode.ID.ObjectPath == "" {
		b.Skip("accessibility snapshot exposes no actionable node")
	}
	for b.Loop() {
		if _, err := session.Accessibility.InvokeDefaultAction(context.Background(), actionNode.ID); err != nil {
			b.Fatalf("accessibility action: %v", err)
		}
	}
}
