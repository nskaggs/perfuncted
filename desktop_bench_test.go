//go:build desktopbench && linux

package perfuncted

import (
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/internal/capturebench"
	"github.com/nskaggs/perfuncted/internal/env"
)

const desktopBenchmarkTimeout = 2 * time.Minute

func desktopCaptureEnvironment() string {
	return fmt.Sprintf("go=%s os=%s/%s cpu=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}

func collectDesktopCaptureSamples(sc capturebench.Screenshotter, rect image.Rectangle, count int) ([]time.Duration, image.Rectangle, error) {
	ctx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
	defer cancel()
	return capturebench.Collect(ctx, sc, rect, count, desktopBenchmarkTimeout)
}

func desktopBenchmarkOptions(logDir string, accessibilityRequired bool) []Option {
	options := []Option{
		WithHeadless(SessionConfig{Resolution: image.Pt(1024, 768), LogDir: logDir}),
		Require(CapabilityScreen, CapabilityInput, CapabilityWindows),
	}
	if accessibilityRequired {
		options = append(options, Require(CapabilityAccessibility))
	}
	return options
}

func openDesktopBenchmarkSession(b *testing.B, logDir string, accessibilityRequired bool) *Session {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
	defer cancel()
	session, err := Open(ctx, desktopBenchmarkOptions(logDir, accessibilityRequired)...)
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

// BenchmarkDesktopCapture measures actual Screenshotter.Grab results in the
// existing private Sway session. Hash-only work remains in its own benchmark.
func BenchmarkDesktopCapture(b *testing.B) {
	if os.Getenv("PERFUNCTED_CAPTURE_BENCH_REAL") != "1" {
		b.Skip("real capture requires PERFUNCTED_CAPTURE_BENCH_REAL=1; no desktop capture attempted")
	}
	for _, variant := range []struct {
		name string
		rect image.Rectangle
	}{
		{name: "WarmFull"},
		{name: "WarmRegion256", rect: image.Rect(0, 0, 256, 256)},
		{name: "WarmRegionLarge", rect: image.Rect(0, 0, 1024, 720)},
	} {
		b.Run(variant.name, func(b *testing.B) {
			session := openDesktopBenchmarkSession(b, b.TempDir(), false)
			firstCtx, firstCancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
			first, err := session.Screen.backend.Grab(firstCtx, image.Rectangle{})
			firstCtxErr := firstCtx.Err()
			firstCancel()
			if err == nil && firstCtxErr != nil {
				err = firstCtxErr
			}
			if err != nil {
				b.Fatalf("first full screenshot: %v", err)
			}
			if first == nil || first.Bounds().Empty() {
				b.Fatalf("invalid first screenshot: %v", first)
			}
			if variant.rect != (image.Rectangle{}) && (variant.rect.Max.X > first.Bounds().Dx() || variant.rect.Max.Y > first.Bounds().Dy()) {
				b.Skipf("region %v exceeds output bounds %v", variant.rect, first.Bounds())
			}
			if variant.rect != (image.Rectangle{}) {
				if _, _, err := collectDesktopCaptureSamples(session.Screen.backend, variant.rect, 1); err != nil {
					b.Fatalf("region warmup: %v", err)
				}
			}
			durations, returned, err := collectDesktopCaptureSamples(session.Screen.backend, variant.rect, 50)
			if err != nil {
				b.Fatalf("distribution samples: %v", err)
			}
			if variant.rect == (image.Rectangle{}) && returned != first.Bounds() {
				b.Fatalf("full-screen geometry changed from %v to %v", first.Bounds(), returned)
			}
			b.Logf("source=real-headless mode=%s backend=%T compositor=sway requested=%v returned=%v desktop=%v %s", variant.name, session.Screen.backend, variant.rect, returned, first.Bounds(), desktopCaptureEnvironment())
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()
				captureCtx, captureCancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
				b.StartTimer()
				img, err := session.Screen.backend.Grab(captureCtx, variant.rect)
				captureErr := captureCtx.Err()
				b.StopTimer()
				captureCancel()
				if err == nil && captureErr != nil {
					err = captureErr
				}
				if err != nil {
					b.Fatalf("screenshot: %v", err)
				}
				if img == nil || img.Bounds().Empty() {
					b.Fatalf("invalid screenshot: %v", img)
				}
				if img.Bounds() != first.Bounds() && variant.rect == (image.Rectangle{}) {
					b.Fatalf("full-screen geometry changed from %v to %v", first.Bounds(), img.Bounds())
				}
				b.StartTimer()
			}
			distribution, err := capturebench.Summarize(durations)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(distribution.Count), "samples")
			b.ReportMetric(float64(distribution.Min.Nanoseconds()), "min-ns")
			b.ReportMetric(float64(distribution.P50.Nanoseconds()), "p50-ns")
			if distribution.HasP95 {
				b.ReportMetric(float64(distribution.P95.Nanoseconds()), "p95-ns")
			}
			b.ReportMetric(float64(distribution.Max.Nanoseconds()), "max-ns")
		})
	}
	b.Run("SessionFacadeWarmFull", func(b *testing.B) {
		session := openDesktopBenchmarkSession(b, b.TempDir(), false)
		if _, _, err := collectDesktopCaptureSamples(session.Screen, image.Rectangle{}, 1); err != nil {
			b.Fatalf("facade warmup: %v", err)
		}
		durations, bounds, err := collectDesktopCaptureSamples(session.Screen, image.Rectangle{}, 50)
		if err != nil {
			b.Fatalf("facade distribution samples: %v", err)
		}
		b.Logf("source=real-headless mode=SessionFacadeWarmFull backend=%T compositor=sway returned=%v %s", session.Screen.backend, bounds, desktopCaptureEnvironment())
		distribution, err := capturebench.Summarize(durations)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			b.StopTimer()
			captureCtx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
			b.StartTimer()
			img, err := session.Screen.Grab(captureCtx, image.Rectangle{})
			captureErr := captureCtx.Err()
			b.StopTimer()
			cancel()
			if err == nil && captureErr != nil {
				err = captureErr
			}
			if err != nil {
				b.Fatal(err)
			}
			if img == nil {
				b.Fatal("facade returned nil image")
			}
			if img.Bounds() != bounds {
				b.Fatalf("facade capture geometry changed from %v to %v", bounds, img.Bounds())
			}
			b.StartTimer()
		}
		b.ReportMetric(float64(distribution.Count), "samples")
		b.ReportMetric(float64(distribution.Min.Nanoseconds()), "min-ns")
		b.ReportMetric(float64(distribution.P50.Nanoseconds()), "p50-ns")
		if distribution.HasP95 {
			b.ReportMetric(float64(distribution.P95.Nanoseconds()), "p95-ns")
		}
		b.ReportMetric(float64(distribution.Max.Nanoseconds()), "max-ns")
	})

	b.Run("FirstFull", func(b *testing.B) {
		b.ReportAllocs()
		loggedBackend := false
		for b.Loop() {
			b.StopTimer()
			ctx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
			session, err := Open(ctx, desktopBenchmarkOptions(b.TempDir(), false)...)
			if err != nil {
				b.Fatalf("open session: %v", err)
			}
			captureCtx, captureCancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
			b.StartTimer()
			img, captureErr := session.Screen.backend.Grab(captureCtx, image.Rectangle{})
			captureContextErr := captureCtx.Err()
			b.StopTimer()
			captureCancel()
			cancel()
			if captureErr == nil && captureContextErr != nil {
				captureErr = captureContextErr
			}
			closeErr := session.Close()
			if captureErr != nil {
				b.Fatalf("first full screenshot: %v", captureErr)
			}
			if img == nil || img.Bounds().Empty() {
				b.Fatalf("invalid first screenshot: %v", img)
			}
			if closeErr != nil {
				b.Fatalf("close session: %v", closeErr)
			}
			if !loggedBackend {
				b.Logf("source=real-headless mode=FirstFull backend=%T compositor=sway requested=%v returned=%v %s", session.Screen.backend, image.Rectangle{}, img.Bounds(), desktopCaptureEnvironment())
				loggedBackend = true
			}
			b.StartTimer()
		}
	})
	b.Run("OpenAndFirstFull", func(b *testing.B) {
		b.ReportAllocs()
		loggedBackend := false
		for b.Loop() {
			b.StopTimer()
			ctx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
			b.StartTimer()
			session, err := Open(ctx, desktopBenchmarkOptions(b.TempDir(), false)...)
			if err != nil {
				b.StopTimer()
				cancel()
				b.Fatalf("open headless session: %v", err)
			}
			b.StopTimer()
			captureCtx, captureCancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
			b.StartTimer()
			img, captureErr := session.Screen.backend.Grab(captureCtx, image.Rectangle{})
			captureContextErr := captureCtx.Err()
			b.StopTimer()
			captureCancel()
			cancel()
			if captureErr == nil && captureContextErr != nil {
				captureErr = captureContextErr
			}
			closeErr := session.Close()
			if captureErr != nil {
				b.Fatalf("first full screenshot: %v", captureErr)
			}
			if img == nil || img.Bounds().Empty() {
				b.Fatalf("invalid first screenshot: %v", img)
			}
			if closeErr != nil {
				b.Fatalf("close session: %v", closeErr)
			}
			if !loggedBackend {
				b.Logf("source=real-headless mode=OpenAndFirstFull backend=%T compositor=sway requested=%v returned=%v %s", session.Screen.backend, image.Rectangle{}, img.Bounds(), desktopCaptureEnvironment())
				loggedBackend = true
			}
			b.StartTimer()
		}
	})
}

func launchDesktopBenchmarkWindow(b *testing.B, session *Session, accessibility bool) (*Application, *Window) {
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
		command := Command{
			Name:   candidate.name,
			Args:   candidate.args,
			Stdout: nil,
			Stderr: nil,
		}
		if accessibility {
			switch candidate.name {
			case "kwrite":
				command.Args = nil
				command.Env = env.Merge(session.Env(),
					"QT_ACCESSIBILITY=1",
					"QT_LINUX_ACCESSIBILITY_ALWAYS_ON=1",
				)
			case "gnome-text-editor":
				command.Env = env.Merge(session.Env(), "GTK_A11Y=atspi")
			}
		}
		app, err := session.Launch(context.Background(), command)
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
	_, window := launchDesktopBenchmarkWindow(b, session, false)
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
	_, window := launchDesktopBenchmarkWindow(b, session, false)
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
		b.Fatalf("accessibility capability required for benchmark: %v", status.Failure)
	}
	ctx, cancel := context.WithTimeout(context.Background(), desktopBenchmarkTimeout)
	defer cancel()
	// Subscribe before launch so the benchmark starts its snapshot only after
	// AT-SPI reports application-tree changes. The application list remains the
	// authoritative readiness check because the event stream is lossy.
	events, err := session.Accessibility.Events(ctx, accessibility.EventOptions{})
	if err != nil {
		b.Fatalf("start AT-SPI benchmark readiness events: %v", err)
	}
	app, _ := launchDesktopBenchmarkWindow(b, session, true)
	if app == nil || app.PID() <= 0 {
		b.Fatal("accessibility benchmark application has no managed process identity")
	}
	for {
		apps, err := session.Accessibility.Applications(ctx)
		if err != nil {
			b.Fatalf("list AT-SPI benchmark applications: %v", err)
		}
		for _, candidate := range apps {
			if candidate.PID == int32(app.PID()) {
				return session, candidate.ID
			}
		}
		select {
		case _, ok := <-events:
			if !ok {
				b.Fatal("AT-SPI readiness event stream closed before application registration")
			}
		case <-ctx.Done():
			b.Fatalf("AT-SPI application %d did not register before benchmark deadline: %v", app.PID(), ctx.Err())
		}
	}
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
