package screen

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/dbusutil"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// A valid screenshot authorization and a valid local file path do not imply
// that the portal supplied a valid image. Failed decoding must not publish
// pixels or strand the capture gate for the next request.
func TestScreenshotPortalRejectsAuthorizedCorruptPNGAndRecovers(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	bad := filepath.Join(t.TempDir(), "invalid-capture.png")
	if err := os.WriteFile(bad, []byte("not a PNG, despite being a portal-approved screenshot"), 0600); err != nil {
		t.Fatal(err)
	}
	portal.mu.Lock()
	portal.uri = "file://" + bad
	portal.mu.Unlock()
	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if err == nil || img != nil || !strings.Contains(err.Error(), "decode PNG") {
		t.Fatalf("malformed authorized image accepted: image=%v err=%v", img, err)
	}
	if len(backend.captureGate) != 0 {
		t.Fatal("malformed screenshot left capture gate held")
	}
	good := filepath.Join(t.TempDir(), "recovered.png")
	file, err := os.Create(good)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	portal.mu.Lock()
	portal.uri = "file://" + good
	portal.mu.Unlock()
	img, err = backend.Grab(context.Background(), image.Rectangle{})
	if err != nil || img == nil || img.Bounds().Dx() != 3 || img.Bounds().Dy() != 2 {
		t.Fatalf("portal remained unusable after malformed PNG: image=%v err=%v", img, err)
	}
	if len(backend.captureGate) != 0 {
		t.Fatal("successful screenshot left capture gate held")
	}
}

// A portal can disappear after construction but before capture authorization.
// The original client remains connected to the session bus; it must still
// refuse pixels instead of trusting a stale previously resolved service.
func TestPortalCaptureRejectsRevokedServiceOwnerAfterConstruction(t *testing.T) {
	backend, provider := newSyntheticScreenshotPortal(t)
	if !backend.conn.Connected() {
		t.Fatal("test screenshot session disconnected before revocation")
	}
	reply, err := provider.conn.ReleaseName(portalDest)
	if err != nil || reply != dbus.ReleaseNameReplyReleased {
		t.Fatalf("revoke screenshot service: %v %v", reply, err)
	}
	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if img != nil || err == nil || !strings.Contains(err.Error(), "resolve screenshot signal owner") {
		t.Fatalf("revoked portal owner allowed capture: img=%v err=%v", img, err)
	}
	if !backend.conn.Connected() {
		t.Fatal("service revocation unexpectedly disconnected client's separate session bus")
	}
}

// Release must disconnect the private D-Bus client, not its provider, so a
// discarded screenshot backend cannot continue submitting portal requests.
func TestPortalScreenshotCloseDisconnectsOnlyClient(t *testing.T) {
	var absent *PortalDBusBackend
	if err := absent.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	if err := (&PortalDBusBackend{}).Close(); err != nil {
		t.Fatalf("missing connection close: %v", err)
	}
	backend, provider := newSyntheticScreenshotPortal(t)
	conn := backend.conn
	if conn == nil || !conn.Connected() {
		t.Fatal("private screenshot bus did not connect")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("closing screenshot client: %v", err)
	}
	if conn.Connected() {
		t.Fatal("screenshot connection remained live after close")
	}
	if provider.conn == nil || !provider.conn.Connected() {
		t.Fatal("closing screenshot client disrupted independent provider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0)
	if result.Err == nil {
		t.Fatal("closed screenshot client could still query session bus")
	}
	if err := backend.Close(); err != nil && !errors.Is(err, dbus.ErrClosed) {
		t.Fatalf("repeated portal close: %v", err)
	}
}

// A portal-supplied URI cannot use non-hexadecimal percent encodings to
// smuggle an attacker-controlled local screenshot path through decoding.
func TestPortalScreenshotRejectsNonHexPercentEscapesAndPreservesValidEscapes(t *testing.T) {
	for _, uri := range []string{
		"file:///var/tmp/screenshot%GG.png",
		"file:///var/tmp/screenshot%2G.png",
		"file:///var/tmp/screenshot%G2.png",
		"file:///var/tmp/screenshot%zz.png",
	} {
		t.Run(uri, func(t *testing.T) {
			path, err := fileURIPath(uri)
			if path != "" || err == nil || !strings.Contains(err.Error(), "invalid escape") {
				t.Fatalf("invalid encoded path accepted: path=%q err=%v", path, err)
			}
		})
	}
	path, err := fileURIPath("file:///var/tmp/screenshot%25literal.png")
	if err != nil || path != "/var/tmp/screenshot%literal.png" {
		t.Fatalf("valid literal percent escape rejected: path=%q err=%v", path, err)
	}
}

// Well-known bus names are not authentication identities. The portal request
// handle must be derived from a private unique name, never a service alias.
func TestPortalScreenshotRejectsMissingUniqueBusNameAndNullSignals(t *testing.T) {
	if name, err := portalUniqueName([]string{"org.freedesktop.portal.Desktop", "org.example.Application"}); name != "" || err == nil || !strings.Contains(err.Error(), "no unique") {
		t.Fatalf("service alias used as request identity: name=%q err=%v", name, err)
	}
	trusted := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_44/pf123")
	if portalSignalMatches(nil, trusted) {
		t.Fatal("null response signal matched a trusted request path")
	}
	if portalSignalMatches(&dbus.Signal{Path: trusted}, dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_44/pf456")) {
		t.Fatal("unrelated response handle matched authenticated request")
	}
	if !portalSignalMatches(&dbus.Signal{Path: trusted}, dbus.ObjectPath(""), trusted) {
		t.Fatal("valid returned handle not accepted after empty optional handle")
	}
}

// A reachable D-Bus session does not imply that the screenshot portal service
// is still present. When its name is relinquished, new capture backends must
// fail closed rather than advertising a screenshot capability.
func TestPortalConstructorRejectsRevokedPrivateScreenshotService(t *testing.T) {
	_, provider := newSyntheticScreenshotPortal(t)
	reply, err := provider.conn.ReleaseName(portalDest)
	if err != nil || reply != dbus.ReleaseNameReplyReleased {
		t.Fatalf("release synthetic portal service: %v %v", reply, err)
	}
	backend, err := NewPortalDBusBackendForBusContext(context.Background(), provider.busAddress)
	if backend != nil {
		_ = backend.Close()
		t.Fatal("absent screenshot portal was authorized")
	}
	if err == nil || !strings.Contains(err.Error(), "not on session bus") {
		t.Fatalf("missing portal not rejected: %v", err)
	}
}

func TestSyntheticScreenshotPortalRejectsMalformedAuthorizationCode(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	path := filepath.Join(t.TempDir(), "valid.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if encodeErr := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	// A malicious or incompatible portal must not be able to return an
	// untyped/string authorization status and have it interpreted as "granted".
	portal.mu.Lock()
	portal.uri = "file://" + path
	portal.malformedCode = "0"
	portal.mu.Unlock()

	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if err == nil || img != nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("screenshot accepted malformed permission status: image=%v err=%v", img, err)
	}
}

func TestPortalScreenshotTimeoutExplainsConsentAndReadOnlyAlternative(t *testing.T) {
	err := portalScreenshotTimeoutError(15*time.Second, portalProtocolTimeout, 15*time.Second)
	if err == nil {
		t.Fatal("portal timeout must be an error")
	}
	for _, want := range []string{
		"timed out waiting for screenshot",
		"policy/protocol 15s/30s, effective 15s",
		"screenshot permission",
		"pf output list",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("timeout %q missing %q", err, want)
		}
	}
}

// A missing capture backend has no diagnostic state and must not attempt
// initialization, even if callers exercise optional methods through a typed
// nil interface value.
func TestNilPortalBackendDiagnosticsAndConfigurationFailClosed(t *testing.T) {
	var backend *PortalDBusBackend
	backend.SetCaptureTimeout(time.Second)
	backend.recordFailure(errors.New("private screenshot name should not be retained"))
	if got := backend.Diagnostics(); !reflect.DeepEqual(got, []string{"backend unavailable"}) {
		t.Fatalf("unavailable portal exposed diagnostic state: %q", got)
	}
	// A nil error should never overwrite the last meaningful observation.
	live := &PortalDBusBackend{}
	live.recordFailure(errors.New("synthetic capture failure"))
	live.recordFailure(nil)
	for _, line := range live.Diagnostics() {
		if line == "last failure: synthetic capture failure" {
			return
		}
	}
	t.Fatalf("nil error erased portal failure: %q", live.Diagnostics())
}

// Cancellation before portal setup must take precedence over a missing or
// invalid bus address and must not create any D-Bus connection.
func TestPortalBackendConstructionRejectsPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, addr := range []string{"", "unix:path=/nonexistent/perfuncted-portal.sock"} {
		backend, err := NewPortalDBusBackendForBusContext(ctx, addr)
		if backend != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled portal construction leaked backend=%v error=%v address=%q", backend, err, addr)
		}
	}
}

// Portal Screenshot responds with a file URI, not a pathname. Unescaped query
// and fragment delimiters do not belong to its file path and must be rejected
// rather than accidentally interpreted as literal filesystem characters.
func TestPortalScreenshotURIRejectsUnescapedQueryAndFragment(t *testing.T) {
	for _, uri := range []string{
		"file:///var/tmp/capture.png?request=secret",
		"file:///var/tmp/capture.png#thumbnail",
		"file://localhost/var/tmp/capture.png?unexpected",
	} {
		if path, err := fileURIPath(uri); err == nil || path != "" {
			t.Errorf("ambiguous screenshot URI %q accepted as %q err=%v", uri, path, err)
		}
	}
	for _, tc := range []struct{ uri, path string }{
		{"file:///var/tmp/name%3Fpart.png", "/var/tmp/name?part.png"},
		{"file:///var/tmp/name%23part.png", "/var/tmp/name#part.png"},
	} {
		path, err := fileURIPath(tc.uri)
		if err != nil || path != tc.path {
			t.Errorf("escaped literal filename %q decoded path=%q err=%v", tc.uri, path, err)
		}
	}
}

func TestGnomeShellScreenshotBackendRejectsUninitializedState(t *testing.T) {
	b := &GnomeShellScreenshotBackend{}

	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err == nil {
		t.Fatal("Grab on an uninitialized backend succeeded")
	}
	if _, _, err := b.Resolution(); err == nil {
		t.Fatal("Resolution on an uninitialized backend succeeded")
	}
}

func TestFileURIPath(t *testing.T) {
	path, err := fileURIPath("file:///tmp/portal%20shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/portal shot.png" {
		t.Fatalf("path = %q", path)
	}
}

func TestFileURIPathRejectsUnsupportedHost(t *testing.T) {
	if _, err := fileURIPath("file://remotehost/tmp/portal.png"); err == nil {
		t.Fatal("expected error for unsupported host")
	}
}

func TestPortalUniqueNamePrefersUniqueBusName(t *testing.T) {
	got, err := portalUniqueName([]string{"org.freedesktop.portal.Desktop", ":1.198", "org.example.App"})
	if err != nil {
		t.Fatalf("portalUniqueName: %v", err)
	}
	if got != ":1.198" {
		t.Fatalf("portalUniqueName = %q, want %q", got, ":1.198")
	}
}

func TestPortalRequestPath(t *testing.T) {
	got := portalRequestPath(":1.198", "pf123")
	want := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_198/pf123")
	if got != want {
		t.Fatalf("portalRequestPath = %q, want %q", got, want)
	}
}

func TestPortalSignalMatchesReturnedHandle(t *testing.T) {
	expected := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_198/pf123")
	returned := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_198/pf999")
	sig := &dbus.Signal{Path: returned}
	if !portalSignalMatches(sig, expected, returned) {
		t.Fatal("portalSignalMatches should accept the returned handle path")
	}
	if portalSignalMatches(sig, expected) {
		t.Fatal("portalSignalMatches unexpectedly matched the wrong expected path")
	}
}

func TestPortalCaptureTimeoutHonorsPolicyAndProtocolCeiling(t *testing.T) {
	tests := []struct {
		name   string
		policy time.Duration
		want   time.Duration
	}{
		{name: "default protocol ceiling", policy: 0, want: portalProtocolTimeout},
		{name: "shorter policy", policy: 2 * time.Second, want: 2 * time.Second},
		{name: "longer policy capped", policy: 2 * time.Minute, want: portalProtocolTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := portalCaptureTimeout(tt.policy); got != tt.want {
				t.Fatalf("portalCaptureTimeout(%s) = %s, want %s", tt.policy, got, tt.want)
			}
		})
	}
}

func TestPortalDiagnosticsDeclareCostAndWaitLimitation(t *testing.T) {
	b := &PortalDBusBackend{}
	diagnostics := b.Diagnostics()
	joined := strings.Join(diagnostics, "\n")
	for _, want := range []string{
		"full-screen PNG",
		"serialized",
		"high-frequency waits",
		"capture timeout: policy",
		"protocol ceiling",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diagnostics %q missing %q", joined, want)
		}
	}
	if slices.Contains(b.SupportedOperations(), "wait") {
		t.Fatalf("portal operations = %v, unexpectedly advertises wait", b.SupportedOperations())
	}
}

func TestRemoveScreenshotIfOwned(t *testing.T) {
	owned := filepath.Join(t.TempDir(), "owned.png")
	if err := os.WriteFile(owned, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeScreenshotIfOwned(owned, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned screenshot still exists: %v", err)
	}

	foreign := filepath.Join(t.TempDir(), "foreign.png")
	if err := os.WriteFile(foreign, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeScreenshotIfOwned(foreign, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign screenshot was removed: %v", err)
	}
}

func TestOpenScreenshotFileRejectsSymlinkOutsideParent(t *testing.T) {
	parent := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "outside.png")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	insidePath := filepath.Join(parent, "inside.png")
	if err := os.WriteFile(insidePath, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, root, err := openScreenshotFile(insidePath)
	if err != nil {
		t.Fatalf("openScreenshotFile(%q): %v", insidePath, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close screenshot: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("close root: %v", err)
	}

	symlinkPath := filepath.Join(parent, "escape.png")
	if err := os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openScreenshotFile(symlinkPath); err == nil {
		t.Fatalf("openScreenshotFile(%q) unexpectedly followed an external symlink", symlinkPath)
	}
}

type emptyStringScreenshotPortal struct {
	conn *dbus.Conn
	code uint32
	uri  string
}

func (p *emptyStringScreenshotPortal) Screenshot(sender dbus.Sender, _ string, opts map[string]dbus.Variant) (string, *dbus.Error) {
	token, ok := opts["handle_token"].Value().(string)
	if !ok {
		return "", dbus.MakeFailedError(errors.New("missing screenshot handle token"))
	}
	path := portalRequestPath(string(sender), token)
	results := map[string]dbus.Variant{"uri": dbus.MakeVariant(p.uri)}
	if err := p.conn.Emit(path, portalReqIf+".Response", p.code, results); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return "", nil
}

func TestScreenshotPortalEmptyStringReplyUsesPredictedPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint32
	}{
		{name: "authorized", code: 0},
		{name: "denied", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, portal := newSyntheticScreenshotPortal(t)
			path := filepath.Join(t.TempDir(), "capture.png")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			fixture := &emptyStringScreenshotPortal{conn: portal.conn, code: tc.code, uri: "file://" + path}
			if err := portal.conn.Export(fixture, portalPath, portalSsIf); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, err := backend.Grab(ctx, image.Rectangle{})
			if tc.code == 0 {
				if err != nil || got == nil || got.Bounds() != image.Rect(0, 0, 3, 2) {
					t.Fatalf("authorized screenshot = (%v, %v)", got, err)
				}
			} else if err == nil || got != nil || !strings.Contains(err.Error(), "denied") {
				t.Fatalf("denied screenshot = (%v, %v)", got, err)
			}
			if len(backend.captureGate) != 0 {
				t.Fatal("screenshot request retained its capture gate")
			}
		})
	}
}

// A portal connection is required for each hash entrypoint, just as for a
// screenshot. A missing backend cannot return a synthetic hash of zero as
// success, and pre-canceled requests must not attempt portal access.
func TestPortalHashEntryPointsFailClosedWithoutCaptureAuthority(t *testing.T) {
	for _, tc := range []struct {
		name         string
		backend      *PortalDBusBackend
		ctx          context.Context
		want         string
		wantCanceled bool
	}{
		{name: "nil backend", want: "not initialised"},
		{name: "disconnected backend", backend: &PortalDBusBackend{}, want: "not initialised"},
		{name: "pre-canceled backend", backend: &PortalDBusBackend{}, ctx: canceledPortalHashContext(), wantCanceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			for _, operation := range []struct {
				name string
				call func() (uint32, error)
			}{
				{name: "full hash", call: func() (uint32, error) { return tc.backend.GrabFullHash(ctx) }},
				{name: "region hash", call: func() (uint32, error) { return tc.backend.GrabRegionHash(ctx, image.Rect(1, 2, 3, 4)) }},
			} {
				t.Run(operation.name, func(t *testing.T) {
					hash, err := operation.call()
					if hash != 0 || err == nil {
						t.Fatalf("unauthorized hash returned hash=%08x err=%v", hash, err)
					}
					if tc.wantCanceled {
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("cancelled hash returned %v", err)
						}
					} else if !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("unauthorized hash error=%v, want %q", err, tc.want)
					}
				})
			}
		})
	}
}

func canceledPortalHashContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

type portalAttackObservable struct{ requests chan dbus.ObjectPath }

func (p *portalAttackObservable) Screenshot(_ string, opts map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	token, _ := opts["handle_token"].Value().(string)
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + token)
	select {
	case p.requests <- path:
	default:
	}
	return path, nil
}

// godbus delivers the union of match rules to all local signal channels.
// A second subscription must not allow another bus peer or a different
// method member to impersonate the authoritative screenshot grant.
func TestScreenshotPortalRejectsForgedResponseAndUnrelatedSignalMember(t *testing.T) {
	for _, tc := range []struct {
		name     string
		attacker bool
		member   string
		code     uint32
	}{
		{"forged authorization", true, "Response", 0},
		{"forged denial", true, "Response", 1},
		{"unrelated portal member", false, "Unexpected", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, portal := newSyntheticScreenshotPortal(t)
			provider := &portalAttackObservable{requests: make(chan dbus.ObjectPath, 1)}
			if err := portal.conn.Export(provider, portalPath, portalSsIf); err != nil {
				t.Fatal(err)
			}
			attacker, err := dbusutil.ConnectContext(context.Background(), portal.busAddress)
			if err != nil {
				t.Fatal(err)
			}
			defer attacker.Close()
			imgPath := filepath.Join(t.TempDir(), "private-fake-grant.png")
			file, err := os.Create(imgPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			wide := []dbus.MatchOption{dbus.WithMatchInterface(portalReqIf)}
			if err := b.conn.AddMatchSignalContext(context.Background(), wide...); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = b.conn.RemoveMatchSignalContext(context.Background(), wide...) }()
			ctx, cancel := context.WithTimeout(context.Background(), 160*time.Millisecond)
			defer cancel()
			done := make(chan struct {
				image image.Image
				err   error
			}, 1)
			go func() {
				captured, err := b.Grab(ctx, image.Rectangle{})
				done <- struct {
					image image.Image
					err   error
				}{captured, err}
			}()
			var path dbus.ObjectPath
			select {
			case path = <-provider.requests:
			case <-ctx.Done():
				t.Fatalf("portal screenshot did not reach synthetic service: %v", ctx.Err())
			}
			service := portal.conn
			if tc.attacker {
				service = attacker
			}
			results := map[string]dbus.Variant{"uri": dbus.MakeVariant("file://" + imgPath)}
			if err := service.Emit(path, portalReqIf+"."+tc.member, tc.code, results); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.err == nil || result.image != nil {
					t.Fatalf("unauthorized or unrelated screenshot response returned image: %v %v", result.image, result.err)
				}
				if !errors.Is(result.err, context.DeadlineExceeded) && !strings.Contains(result.err.Error(), "canceled") {
					t.Fatalf("untrusted response was treated as authoritative error: %v", result.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("screenshot request hung instead of honoring canceled authorization")
			}
		})
	}
}

type syntheticScreenshotPortal struct {
	conn          *dbus.Conn
	busAddress    string
	mu            sync.Mutex
	code          uint32
	malformedCode any
	uri           string
	skip          bool
}

func (p *syntheticScreenshotPortal) Screenshot(_ string, opts map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	token, _ := opts["handle_token"].Value().(string)
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + token)
	p.mu.Lock()
	code, uri, skip := any(p.code), p.uri, p.skip
	if p.malformedCode != nil {
		code = p.malformedCode
	}
	p.mu.Unlock()
	if !skip {
		go func() {
			time.Sleep(10 * time.Millisecond)
			results := map[string]dbus.Variant{"uri": dbus.MakeVariant(uri)}
			_ = p.conn.Emit(path, portalReqIf+".Response", code, results)
		}()
	}
	return path, nil
}
func newSyntheticScreenshotPortal(t *testing.T) (*PortalDBusBackend, *syntheticScreenshotPortal) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	dir, err := os.MkdirTemp("", "pf-screen-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "bus")
	addr := "unix:path=" + socket
	proc := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+addr)
	if startErr := proc.Start(); startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	var service *dbus.Conn
	for i := 0; i < 100; i++ {
		service, err = dbusutil.SessionBusAddressContext(context.Background(), addr)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connect private bus: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(portalDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register screenshot service: %v, %v", reply, err)
	}
	portal := &syntheticScreenshotPortal{conn: service, busAddress: addr}
	if exportErr := service.Export(portal, portalPath, portalSsIf); exportErr != nil {
		t.Fatal(exportErr)
	}
	backend, err := NewPortalDBusBackendForBusContext(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend, portal
}

func TestSyntheticScreenshotPortalCaptureCropAndHash(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	path := filepath.Join(t.TempDir(), "capture space.png")
	imageData := image.NewRGBA(image.Rect(0, 0, 4, 4))
	imageData.Set(1, 1, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if encodeErr := png.Encode(file, imageData); encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	portal.mu.Lock()
	portal.uri = "file://" + strings.ReplaceAll(path, " ", "%20")
	portal.mu.Unlock()
	img, err := backend.Grab(context.Background(), image.Rect(1, 1, 3, 3))
	if err != nil {
		t.Fatalf("authorized screenshot: %v", err)
	}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("cropped screenshot bounds %v", img.Bounds())
	}
	if got := color.RGBAModel.Convert(img.At(img.Bounds().Min.X, img.Bounds().Min.Y)).(color.RGBA); got.R != 255 { //nolint:errcheck // RGBAModel.Convert guarantees color.RGBA
		t.Fatalf("unexpected crop pixel %v", got)
	}
	if _, err := backend.GrabRegionHash(context.Background(), image.Rect(1, 1, 3, 3)); err != nil {
		t.Fatalf("region hash: %v", err)
	}
	if _, err := backend.GrabFullHash(context.Background()); err != nil {
		t.Fatalf("full hash: %v", err)
	}
}

func TestSyntheticScreenshotPortalFailsClosedOnPermissionAndFileErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint32
		uri  string
		skip bool
		want string
	}{
		{"permission denied", 1, "", false, "denied"},
		{"non-file URI", 0, "https://invalid.example/capture.png", false, "unsupported"},
		{"missing image", 0, "file:///tmp/perfuncted-unavailable-capture.png", false, "open"},
		{"malformed png", 0, "", false, "parse"},
		{"no response", 0, "", true, "timed out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, portal := newSyntheticScreenshotPortal(t)
			backend.SetCaptureTimeout(70 * time.Millisecond)
			portal.mu.Lock()
			portal.code = tc.code
			portal.uri = tc.uri
			portal.skip = tc.skip
			portal.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := backend.Grab(ctx, image.Rectangle{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Grab error %v, want %q", err, tc.want)
			}
			if !strings.Contains(strings.Join(backend.Diagnostics(), " "), "last failure") {
				t.Fatal("capture failure not reflected in diagnostics")
			}
		})
	}
}

func TestSyntheticScreenshotPortalCancellationAndSerialGate(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	portal.mu.Lock()
	portal.skip = true
	portal.mu.Unlock()
	backend.SetCaptureTimeout(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Grab(ctx, image.Rectangle{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled screenshot returned %v", err)
	}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err := backend.Grab(ctx, image.Rectangle{})
		cancel()
		if err == nil {
			t.Fatal("unanswered portal screenshot unexpectedly succeeded")
		}
	}
}
