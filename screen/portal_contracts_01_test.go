package screen

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
