package window

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/testutil"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const syntheticSwayWindowTree = `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"Editor","app_id":"com.example.Editor","focused":true,"rect":{"x":8,"y":12,"width":960,"height":640}},{"id":43,"type":"floating_con","name":"Terminal","focused":false,"rect":{"x":40,"y":60,"width":800,"height":500}}]}`

func newSyntheticSwayCommands(t *testing.T, tree string, commandReply string) (*SwayManager, func() ([]string, error)) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	var commands []string
	var serverErr error
	go func() {
		defer close(done)
		defer server.Close()
		for {
			typ, payload, err := readSwayMessage(server)
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					serverErr = err
				}
				return
			}
			var response string
			switch typ {
			case swayMsgGetTree:
				commands = append(commands, "GET_TREE")
				response = tree
			case swayMsgRunCommand:
				commands = append(commands, string(payload))
				response = commandReply
			default:
				serverErr = errors.New("unsupported sway IPC message")
				return
			}
			if err := writeSwayMessage(server, typ, response); err != nil {
				serverErr = err
				return
			}
		}
	}()
	manager := &SwayManager{conn: client}
	finish := func() ([]string, error) { _ = manager.Close(); _ = client.Close(); <-done; return commands, serverErr }
	return manager, finish
}

func TestSwayWindowActionsUseSingleRealIPCCommandSequence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*SwayManager) error
		want   []string
	}{
		{"resize", func(m *SwayManager) error { return m.ResizeByID(context.Background(), "42", 320, 200) }, []string{"GET_TREE", "[con_id=42] floating enable", "[con_id=42] resize set 320 200"}},
		{"close", func(m *SwayManager) error { return m.CloseWindowByID(context.Background(), "42") }, []string{"GET_TREE", "[con_id=42] kill"}},
		{"minimize", func(m *SwayManager) error { return m.MinimizeByID(context.Background(), "42") }, []string{"GET_TREE", "[con_id=42] move scratchpad"}},
		{"fullscreen", func(m *SwayManager) error { return m.FullscreenByID(context.Background(), "42") }, []string{"GET_TREE", "[con_id=42] fullscreen enable"}},
		{"unfullscreen", func(m *SwayManager) error { return m.UnfullscreenByID(context.Background(), "42") }, []string{"GET_TREE", "[con_id=42] fullscreen disable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
			err := tc.invoke(m)
			commands, serverErr := finish()
			if err != nil || serverErr != nil || !reflect.DeepEqual(commands, tc.want) {
				t.Fatalf("action failed: err=%v server=%v calls=%q want=%q", err, serverErr, commands, tc.want)
			}
		})
	}
}

func TestSwaySyntheticWindowEnumerationAndActiveTitle(t *testing.T) {
	for _, tc := range []struct {
		name, tree string
		wantTitle  string
		wantErr    bool
	}{
		{"focused", syntheticSwayWindowTree, "Editor", false},
		{"no focus", `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"Editor"}]}`, "", true},
		{"malformed tree", "not-json", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, tc.tree, `[{"success":true}]`)
			title, err := m.ActiveTitle(context.Background())
			commands, serverErr := finish()
			if serverErr != nil || title != tc.wantTitle || (err != nil) != tc.wantErr || !reflect.DeepEqual(commands, []string{"GET_TREE"}) {
				t.Fatalf("ActiveTitle got %q %v commands=%v server=%v", title, err, commands, serverErr)
			}
		})
	}
	m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
	windows, err := m.List(context.Background())
	if err != nil || len(windows) != 2 || windows[0].Title != "Editor" || windows[1].Title != "Terminal" {
		t.Fatalf("wrong windows: %+v %v", windows, err)
	}
	info, err := m.InfoByID(context.Background(), "43")
	if err != nil || info.Title != "Terminal" || info.ID != 43 || info.X != 40 {
		t.Fatalf("InfoByID: %+v %v", info, err)
	}
	if _, err := m.InfoByID(context.Background(), "invalid-id"); err == nil {
		t.Fatal("unsafe ID reached IPC")
	}
	if _, err := m.InfoByID(context.Background(), "999"); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("missing window: %v", err)
	}
	cmds, serverErr := finish()
	if serverErr != nil || !reflect.DeepEqual(cmds, []string{"GET_TREE", "GET_TREE", "GET_TREE"}) {
		t.Fatalf("unexpected query sequence %v %v", cmds, serverErr)
	}
}

func TestSwaySyntheticActionFailuresNeverAcknowledgeMutation(t *testing.T) {
	for _, reply := range []string{`[{"success":false,"error":"permission denied"}]`, `[]`, `not-json`} {
		t.Run(reply, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, reply)
			err := m.CloseWindowByID(context.Background(), "42")
			commands, serverErr := finish()
			if err == nil || serverErr != nil || len(commands) != 2 || !strings.Contains(commands[1], "kill") {
				t.Fatalf("failed command acknowledged: %v calls=%v server=%v", err, commands, serverErr)
			}
		})
	}
	for _, id := range []string{"; focus", "-7", "999999999999999999999"} {
		t.Run("unsafe_"+id, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
			err := m.CloseWindowByID(context.Background(), id)
			cmds, serverErr := finish()
			if err == nil || serverErr != nil || len(cmds) != 0 {
				t.Fatalf("invalid ID dispatched: err=%v cmds=%v", err, cmds)
			}
		})
	}
}

func TestSwayRuntimeDiscoveryFindsSyntheticSocketWithoutHostDisplay(t *testing.T) {
	// Missing SWAYSOCK must fail closed when no synthetic socket is advertised.
	if _, err := NewSwayManagerRuntimeContext(context.Background(), env.FromEnviron([]string{})); err == nil {
		t.Fatal("missing runtime unexpectedly fell back to host")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSwayManagerRuntimeContext(canceled, env.FromEnviron([]string{"SWAYSOCK=/no/such/socket"})); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery=%v", err)
	}
}

func TestSwayResizeInvalidDimensionsNeverDispatchesIPC(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"negative width", -1, 300}, {"zero width", 0, 300}, {"zero height", 300, 0}, {"negative height", 300, -2}, {"overflow width", 1 << 33, 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
			err := m.ResizeByID(context.Background(), "42", tc.w, tc.h)
			cmds, serverErr := finish()
			if err == nil || serverErr != nil || len(cmds) != 0 {
				t.Fatalf("invalid resize %dx%d hit IPC (%v), err=%v", tc.w, tc.h, cmds, err)
			}
		})
	}
}

func TestSwayRuntimeDiscoversPrivateSocketAndFallsBackFromUnreachableExplicitSocket(t *testing.T) {
	for _, withExplicit := range []bool{true, false} {
		t.Run(map[bool]string{true: "explicit", false: "runtime_glob_fallback"}[withExplicit], func(t *testing.T) {
			dir := testutil.ShortSocketDir(t)
			sock := filepath.Join(dir, "sway-ipc.123.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, lintShadow186 := listener.AcceptUnix()
				if lintShadow186 != nil {
					serverDone <- lintShadow186
					return
				}
				defer conn.Close()
				kind, _, lintShadow186 := readSwayMessage(conn)
				if lintShadow186 != nil {
					serverDone <- lintShadow186
					return
				}
				if kind != swayMsgGetTree {
					serverDone <- errors.New("unexpected probe IPC kind")
					return
				}
				serverDone <- writeSwayMessage(conn, swayMsgGetTree, syntheticSwayWindowTree)
			}()
			vars := []string{"XDG_RUNTIME_DIR=" + dir}
			if withExplicit {
				vars = append(vars, "SWAYSOCK="+sock)
			} else {
				vars = append(vars, "SWAYSOCK="+filepath.Join(dir, "not-sway.sock"))
			}
			manager, err := NewSwayManagerRuntimeContext(context.Background(), env.FromEnviron(vars))
			if err != nil || manager == nil || manager.sock != sock {
				t.Fatalf("runtime did not discover reachable private Sway socket: mgr=%+v err=%v", manager, err)
			}
			if err := manager.Close(); err != nil {
				t.Fatal(err)
			}
			if err := <-serverDone; err != nil {
				t.Fatalf("probe response failed: %v", err)
			}
		})
	}
}

// Public window lookups accept nil as the default context. In particular,
// cancellation checks after a successful IPC tree read must not dereference
// the original nil argument.
func TestSwayActiveTitleNilContextUsesDefaultWithoutPanic(t *testing.T) {
	m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
	defer func() {
		if _, err := finish(); err != nil {
			t.Error(err)
		}
	}()
	title, err := m.ActiveTitle(nil)
	if err != nil || title != "Editor" {
		t.Fatalf("nil-context active title=%q err=%v", title, err)
	}
}

// A compositor response cannot establish an authoritative active title if
// multiple distinct windows claim focus; choosing the first leaks a false
// focus observation to automation clients.
func TestSwayActiveTitleRejectsConflictingFocusedWindows(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":41,"type":"con","name":"first private window","focused":true},{"id":42,"type":"con","name":"second private window","focused":true}]}`
	manager, finish := newSyntheticSwayCommands(t, tree, `[{"success":true}]`)
	title, err := manager.ActiveTitle(context.Background())
	if title != "" || err == nil || !strings.Contains(err.Error(), "multiple focused") {
		t.Fatalf("ambiguous Sway focus incorrectly trusted: title=%q err=%v", title, err)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}

// Subscribing after shutdown must produce a terminal stream without ever
// dialing the target socket. Even a repeated subscription stays terminal.
func TestSwayWindowChangesAfterCloseNeverDialsAndStaysClosed(t *testing.T) {
	original := swayDialContext
	t.Cleanup(func() { swayDialContext = original })
	dials := 0
	swayDialContext = func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("must not dial")
	}
	manager := &SwayManager{sock: "no-target"}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		ch := manager.WindowChanges()
		if ch == nil {
			t.Fatal("closed manager returned nil stream")
		}
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("closed manager published event on attempt %d", i)
			}
		case <-time.After(time.Second):
			t.Fatal("closed subscription did not terminate immediately")
		}
	}
	if dials != 0 {
		t.Fatalf("closed manager unexpectedly dialed %d times", dials)
	}
}

// Active admission checks closure before installing the shared query socket.
// Shutdown and a rejected query must never leave the active connection set.
func TestSwayBeginQueryAfterCloseDoesNotInstallConnection(t *testing.T) {
	m := &SwayManager{}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	m.signalClosed()
	if m.beginQuery(client) {
		t.Fatal("closed manager admitted active query")
	}
	if m.activeConn != nil {
		t.Fatal("closed manager retained active query")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

type stubSwayAddr string

func (a stubSwayAddr) Network() string { return "unix" }
func (a stubSwayAddr) String() string  { return string(a) }

type stubSwayConn struct {
	mu               sync.Mutex
	deadline         time.Time
	setDeadlineCalls int
	writeCalls       int
	writeLimit       int
	closed           bool
}

func (c *stubSwayConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (c *stubSwayConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeCalls++
	if c.writeLimit > 0 && len(p) > c.writeLimit {
		return c.writeLimit, nil
	}
	return len(p), nil
}

func (c *stubSwayConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *stubSwayConn) LocalAddr() net.Addr                { return stubSwayAddr("local") }
func (c *stubSwayConn) RemoteAddr() net.Addr               { return stubSwayAddr("remote") }
func (c *stubSwayConn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *stubSwayConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

func (c *stubSwayConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	c.setDeadlineCalls++
	return nil
}

func (c *stubSwayConn) snapshot() (time.Time, int, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline, c.setDeadlineCalls, c.writeCalls, c.closed
}

type successSwayConn struct {
	stubSwayConn
	readBuf bytes.Buffer
}

func newSuccessSwayConn(body []byte) *successSwayConn {
	hdr := make([]byte, 14)
	copy(hdr[0:6], swayMagic[:])
	binary.LittleEndian.PutUint32(hdr[6:10], uint32(len(body)))
	binary.LittleEndian.PutUint32(hdr[10:14], swayMsgGetTree)
	c := &successSwayConn{}
	c.readBuf.Write(hdr)
	c.readBuf.Write(body)
	return c
}

func (c *successSwayConn) Read(p []byte) (int, error) {
	return c.readBuf.Read(p)
}

func (c *successSwayConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeCalls++
	return len(p), nil
}

func TestSwayQueryConnCanceledContextShortCircuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn := &stubSwayConn{}
	_, err := swayQueryConn(ctx, conn, swayMsgGetTree, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("swayQueryConn error = %v, want context.Canceled", err)
	}

	_, setDeadlineCalls, writeCalls, closed := conn.snapshot()
	if setDeadlineCalls != 0 || writeCalls != 0 || closed {
		t.Fatalf("swayQueryConn touched connection after cancellation: setDeadline=%d write=%d closed=%v", setDeadlineCalls, writeCalls, closed)
	}
}

func TestSwayActiveTitleCanceledContextShortCircuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn := &stubSwayConn{}
	_, err := (&SwayManager{conn: conn}).ActiveTitle(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ActiveTitle error = %v, want context.Canceled", err)
	}

	_, setDeadlineCalls, writeCalls, closed := conn.snapshot()
	if setDeadlineCalls != 0 || writeCalls != 0 || closed {
		t.Fatalf("ActiveTitle touched connection after cancellation: setDeadline=%d write=%d closed=%v", setDeadlineCalls, writeCalls, closed)
	}
}

func TestSwayIPCRejectsNilConnections(t *testing.T) {
	if err := writeSwayMessage(nil, swayMsgGetTree, ""); err == nil {
		t.Fatal("writeSwayMessage accepted a nil connection")
	}
	if _, err := swayQueryConn(context.Background(), nil, swayMsgGetTree, ""); err == nil {
		t.Fatal("swayQueryConn accepted a nil connection")
	}
}

func TestSwayManagerRejectsOperationsAfterClose(t *testing.T) {
	originalDial := swayDialContext
	t.Cleanup(func() { swayDialContext = originalDial })
	dialCalls := 0
	swayDialContext = func(context.Context, string, string) (net.Conn, error) {
		dialCalls++
		return nil, errors.New("unexpected dial")
	}

	m := &SwayManager{sock: "ignored"}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.Sync(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Sync error = %v, want net.ErrClosed", err)
	}
	if _, err := m.List(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("List error = %v, want net.ErrClosed", err)
	}
	if dialCalls != 0 {
		t.Fatalf("dial calls after close = %d, want 0", dialCalls)
	}
}

func TestSwayManagerCloseClosesTransientQuery(t *testing.T) {
	originalDial := swayDialContext
	t.Cleanup(func() { swayDialContext = originalDial })

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	dialStarted := make(chan struct{})
	swayDialContext = func(context.Context, string, string) (net.Conn, error) {
		close(dialStarted)
		return client, nil
	}

	requestRead := make(chan error, 1)
	go func() {
		_, _, err := readSwayMessage(server)
		requestRead <- err
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &SwayManager{sock: "ignored"}
	queryDone := make(chan error, 1)
	go func() {
		_, err := m.ActiveTitle(ctx)
		queryDone <- err
	}()

	<-dialStarted
	if err := <-requestRead; err != nil {
		t.Fatalf("read query request: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case queryErr := <-queryDone:
		if queryErr == nil {
			t.Fatal("closed Sway connection unexpectedly completed its blocked query")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Close did not unblock the transient query")
	}
}

func TestSwayQueryConnContextCancellationUnblocksRead(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	requestRead := make(chan error, 1)
	go func() {
		_, _, err := readSwayMessage(server)
		requestRead <- err
	}()

	ctx, cancel := context.WithCancel(context.Background())
	queryDone := make(chan error, 1)
	go func() {
		_, err := swayQueryConnContext(ctx, client, swayMsgGetTree, "")
		queryDone <- err
	}()
	if err := <-requestRead; err != nil {
		t.Fatalf("read query request: %v", err)
	}

	cancel()
	select {
	case err := <-queryDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("query error = %v, want context.Canceled", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("context cancellation did not unblock the Sway query")
	}
}

func TestSwayQueryConnUsesContextDeadline(t *testing.T) {
	deadline := time.Now().Add(75 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	conn := &stubSwayConn{}
	_, err := swayQueryConn(ctx, conn, swayMsgGetTree, "")
	if !errors.Is(err, io.EOF) {
		t.Fatalf("swayQueryConn error = %v, want io.EOF", err)
	}

	gotDeadline, setDeadlineCalls, writeCalls, _ := conn.snapshot()
	if setDeadlineCalls == 0 {
		t.Fatal("swayQueryConn did not set a deadline")
	}
	if writeCalls == 0 {
		t.Fatal("swayQueryConn did not attempt to write the IPC request")
	}
	if delta := gotDeadline.Sub(deadline); delta < -20*time.Millisecond || delta > 20*time.Millisecond {
		t.Fatalf("deadline delta = %v, want within +/-20ms of context deadline", delta)
	}
	if until := time.Until(gotDeadline); until > time.Second {
		t.Fatalf("deadline too far in the future: %v", until)
	}
}

func TestSwayQueryAndDialFallbacksRespectPolicyDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	if got := swayQueryDeadline(ctx); !got.Equal(deadline) {
		t.Fatalf("query deadline = %v, want caller policy deadline %v", got, deadline)
	}
	if got := swayDialTimeout(ctx); got != 0 {
		t.Fatalf("dial timeout with caller policy deadline = %s, want no independent timeout", got)
	}
}

func TestSwayQueryAndDialUseDirectCallerSafetyFallbacks(t *testing.T) {
	ctx := context.Background()
	queryDeadline := swayQueryDeadline(ctx)
	queryRemaining := time.Until(queryDeadline)
	if queryRemaining <= 0 || queryRemaining > 5*time.Second {
		t.Fatalf("query fallback remaining = %s, want positive and <= 5s", queryRemaining)
	}
	if got := swayDialTimeout(ctx); got != 5*time.Second {
		t.Fatalf("dial fallback timeout = %s, want 5s", got)
	}
}

func TestWriteSwayMessageRejectsShortWrite(t *testing.T) {
	conn := &stubSwayConn{writeLimit: 1}

	err := writeSwayMessage(conn, swayMsgGetTree, "payload")
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeSwayMessage error = %v, want io.ErrShortWrite", err)
	}
}

func TestSwayQueryConnRejectsUnexpectedResponseType(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	serverDone := make(chan error, 1)
	go func() {
		_, _, err := readSwayMessage(server)
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- writeSwayMessage(server, swayMsgRunCommand, `{"unexpected":true}`)
	}()

	_, err := swayQueryConn(context.Background(), client, swayMsgGetTree, "")
	if err == nil || !strings.Contains(err.Error(), "unexpected response type") {
		t.Fatalf("swayQueryConn error = %v, want unexpected response type", err)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("server: %v", serverErr)
	}
}

func TestSwayCmdRequiresSuccessfulCommandResults(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{name: "empty result list", response: `[]`, want: "no command result"},
		{name: "later command failure", response: `[{"success":true},{"success":false,"error":"rejected"}]`, want: "command failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()

			serverDone := make(chan error, 1)
			go func() {
				_, _, err := readSwayMessage(server)
				if err != nil {
					serverDone <- err
					return
				}
				serverDone <- writeSwayMessage(server, swayMsgRunCommand, tt.response)
			}()

			m := &SwayManager{conn: client}
			err := m.swayCmd(context.Background(), "focus")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("swayCmd error = %v, want %q", err, tt.want)
			}
			if serverErr := <-serverDone; serverErr != nil {
				t.Fatalf("server: %v", serverErr)
			}
		})
	}
}

func TestSwayActivateByIDSendsOnlyFocusCommand(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	serverDone := make(chan error, 1)
	go func() {
		messageType, body, err := readSwayMessage(server)
		if err != nil {
			serverDone <- err
			return
		}
		if messageType != swayMsgRunCommand {
			serverDone <- fmt.Errorf("request type = %d, want RUN_COMMAND", messageType)
			return
		}
		if got := string(body); got != "[con_id=42] focus" {
			serverDone <- fmt.Errorf("command = %q, want %q", got, "[con_id=42] focus")
			return
		}
		serverDone <- writeSwayMessage(server, swayMsgRunCommand, `[ {"success":true} ]`)
	}()

	if err := (&SwayManager{conn: client}).ActivateByID(context.Background(), "42"); err != nil {
		t.Fatalf("ActivateByID: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestSwayActivateByIDRejectsMalformedIDBeforeIPC(t *testing.T) {
	conn := &stubSwayConn{}
	for _, id := range []string{"", "not-a-number", "9223372036854775808"} {
		t.Run(id, func(t *testing.T) {
			conn.mu.Lock()
			conn.writeCalls = 0
			conn.setDeadlineCalls = 0
			conn.mu.Unlock()

			err := (&SwayManager{conn: conn}).ActivateByID(context.Background(), id)
			if err == nil {
				t.Fatal("ActivateByID accepted malformed ID")
			}
			_, deadlineCalls, writeCalls, _ := conn.snapshot()
			if deadlineCalls != 0 || writeCalls != 0 {
				t.Fatalf("malformed ID touched IPC: setDeadline=%d write=%d", deadlineCalls, writeCalls)
			}
		})
	}
}

func TestSwayActivateByIDPropagatesCommandFailureWithoutTreePreflight(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	serverDone := make(chan error, 1)
	go func() {
		messageType, body, err := readSwayMessage(server)
		if err != nil {
			serverDone <- err
			return
		}
		if messageType != swayMsgRunCommand {
			serverDone <- fmt.Errorf("request type = %d, want RUN_COMMAND", messageType)
			return
		}
		if got := string(body); got != "[con_id=999999] focus" {
			serverDone <- fmt.Errorf("command = %q, want %q", got, "[con_id=999999] focus")
			return
		}
		serverDone <- writeSwayMessage(server, swayMsgRunCommand, `[{"success":false,"error":"No matches"}]`)
	}()

	err := (&SwayManager{conn: client}).ActivateByID(context.Background(), "999999")
	if err == nil || !strings.Contains(err.Error(), "command failed: No matches") {
		t.Fatalf("ActivateByID error = %v, want Sway command failure", err)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
}

func TestSwayQueryCancelableContextReusesPersistentConn(t *testing.T) {
	origDial := swayDialContext
	defer func() { swayDialContext = origDial }()

	persistent := newSuccessSwayConn([]byte(`{"ok":true}`))
	dialCalls := 0
	swayDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialCalls++
		return nil, errors.New("unexpected transient dial")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	m := &SwayManager{sock: "ignored", conn: persistent}
	body, err := m.query(ctx, swayMsgGetTree, "")
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("query body = %s, want JSON body", body)
	}
	if _, _, persistentWrites, persistentClosed := persistent.snapshot(); persistentWrites == 0 || persistentClosed {
		t.Fatalf("persistent conn writeCalls=%d closed=%v, want write and reuse", persistentWrites, persistentClosed)
	}
	if dialCalls != 0 {
		t.Fatalf("transient dial calls = %d, want 0", dialCalls)
	}
	if m.conn != persistent {
		t.Fatal("query replaced persistent conn")
	}
}

// Move enables floating and then moves, and nothing else. It used to poll the
// tree for the window after enabling floating, but findByID had already read
// that tree, so the window was always present on the first poll and the wait
// never deferred the move. It cost a full tree round trip per move.
func TestSwayMoveByIDIssuesExactlyOneTreeRead(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	tree := `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"window"}]} `
	var seen []uint32
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for {
			header := make([]byte, 14)
			if _, err := io.ReadFull(server, header); err != nil {
				return
			}
			size := binary.LittleEndian.Uint32(header[6:10])
			messageType := binary.LittleEndian.Uint32(header[10:14])
			body := make([]byte, size)
			if _, err := io.ReadFull(server, body); err != nil {
				return
			}
			seen = append(seen, messageType)
			if messageType == swayMsgGetTree {
				if err := writeSwayMessage(server, messageType, tree); err != nil {
					return
				}
			} else {
				if err := writeSwayMessage(server, messageType, `[{"success":true}]`); err != nil {
					return
				}
			}
		}
	}()

	m := &SwayManager{conn: client}
	if err := m.MoveByID(context.Background(), "42", 10, 20); err != nil {
		t.Fatalf("MoveByID: %v", err)
	}

	want := []uint32{swayMsgGetTree, swayMsgRunCommand, swayMsgRunCommand}
	if !slices.Equal(seen, want) {
		t.Fatalf("sway messages = %v, want %v (one tree read, then floating and move)", seen, want)
	}
	_ = client.Close()
	<-serverDone
}

func TestSwayIterateWindowsPopulatesState(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[` +
		`{"id":42,"type":"con","name":"focused","focused":true,"fullscreen_mode":1},` +
		`{"id":43,"type":"con","name":"normal","focused":false,"fullscreen_mode":0}` +
		`]} `
	conn := newSuccessSwayConn([]byte(tree))

	m := &SwayManager{conn: conn}
	var infos []Info
	for info, err := range m.IterateWindows(context.Background()) {
		if err != nil {
			t.Fatalf("IterateWindows error: %v", err)
		}
		infos = append(infos, info)
	}

	want := map[string]Info{
		"focused": {Title: "focused", Active: true, Fullscreen: true},
		"normal":  {Title: "normal", Active: false, Fullscreen: false},
	}
	if len(infos) != len(want) {
		t.Fatalf("IterateWindows returned %d windows, want %d", len(infos), len(want))
	}
	for _, info := range infos {
		exp, ok := want[info.Title]
		if !ok {
			t.Fatalf("unexpected window %q", info.Title)
		}
		if info.Active != exp.Active || info.Fullscreen != exp.Fullscreen {
			t.Errorf("window %q: Active=%v Fullscreen=%v, want Active=%v Fullscreen=%v",
				info.Title, info.Active, info.Fullscreen, exp.Active, exp.Fullscreen)
		}
	}
}

func TestSwayMaximizeNotAdvertisedOrMappedToFullscreen(t *testing.T) {
	m := &SwayManager{}
	for _, op := range m.SupportedOperations() {
		if op == "maximize" {
			t.Fatal("SupportedOperations still advertises maximize on sway")
		}
	}
	if err := m.MaximizeByID(context.Background(), "42"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("MaximizeByID error = %v, want ErrNotSupported", err)
	}
}

func TestSwayNilReceiverIsSafe(t *testing.T) {
	var manager *SwayManager
	for info, err := range manager.IterateWindows(context.Background()) {
		if info != (Info{}) {
			t.Fatalf("IterateWindows yielded %#v for nil manager", info)
		}
		if err == nil || !strings.Contains(err.Error(), "manager is nil") {
			t.Fatalf("IterateWindows error = %v, want nil-manager error", err)
		}
	}
	if err := manager.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "manager is nil") {
		t.Fatalf("Sync error = %v, want nil-manager error", err)
	}
	if manager.WindowChanges() != nil {
		t.Fatal("WindowChanges returned a channel for a nil manager")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}

// A query holds the IPC gate across a socket round-trip. A caller must not stay
// queued for admission after its own deadline passes: it has no reason to wait
// for work it no longer wants, and reporting the deadline only after an
// unrelated query finished misstates why the operation was abandoned.
func TestSwayQueryDoesNotWaitForGatePastItsDeadline(t *testing.T) {
	m := &SwayManager{sock: "unused"}
	// Hold the gate with an unrelated caller that is never released before the
	// assertion below, so returning can only mean admission was abandoned.
	held, err := m.admit(context.Background())
	if err != nil {
		t.Fatalf("admit first: %v", err)
	}
	defer held()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.List(ctx)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("List error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("List kept waiting for the IPC gate after its own deadline passed")
	}
}

// A dial failure cannot establish a reusable Sway connection. In particular,
// cancellation racing a transport error must retain the caller's context
// reason, and either outcome must leave the manager able to connect later.
func TestSwayQueryDialFailureDoesNotPublishTransportOrLoseCancellation(t *testing.T) {
	sentinel := errors.New("synthetic socket unavailable")
	for _, tc := range []struct {
		name             string
		cancelDuringDial bool
		want             error
	}{
		{"socket unavailable", false, sentinel},
		{"canceled during dial", true, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := swayDialContext
			t.Cleanup(func() { swayDialContext = old })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			swayDialContext = func(_ context.Context, network, address string) (net.Conn, error) {
				calls++
				if network != "unix" || address != "synthetic.sock" {
					t.Fatalf("unexpected transport %q %q", network, address)
				}
				if calls == 1 {
					if tc.cancelDuringDial {
						cancel()
					}
					return nil, sentinel
				}
				return newSuccessSwayConn([]byte(`{"id":1,"type":"root"}`)), nil
			}
			manager := &SwayManager{sock: "synthetic.sock"}
			data, err := manager.query(ctx, swayMsgGetTree, "")
			if len(data) != 0 || !errors.Is(err, tc.want) {
				t.Fatalf("failed dial returned %q, %v; want %v", data, err, tc.want)
			}
			if manager.conn != nil {
				t.Fatal("failed socket was retained as reusable connection")
			}
			if got := calls; got != 1 {
				t.Fatalf("failed dial attempted %d connections", got)
			}
			data, err = manager.query(context.Background(), swayMsgGetTree, "")
			if err != nil || string(data) != `{"id":1,"type":"root"}` {
				t.Fatalf("failed query blocked subsequent recovered connection: %q %v", data, err)
			}
			if calls != 2 {
				t.Fatalf("recovered query made %d dials", calls)
			}
			if err := manager.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

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

// A Sway GET_TREE response must not advertise conflicting windows with the
// same stable identifier. A caller must not act on an ambiguous handle.
func TestSwayTreeRejectsDuplicateActionableWindowIdentities(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"first"}],"floating_nodes":[{"id":42,"type":"floating_con","name":"conflicting"}]}`
	m, finish := newSyntheticSwayCommands(t, tree, `[{"success":true}]`)
	windows, err := m.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "duplicate") || len(windows) != 0 {
		t.Fatalf("duplicated window identity accepted: windows=%+v error=%v", windows, err)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}

func TestSwayWindowChangesUsesDedicatedSubscription(t *testing.T) {
	originalDial := swayDialContext
	t.Cleanup(func() {
		swayDialContext = originalDial
	})

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
	})
	swayDialContext = func(
		context.Context,
		string,
		string,
	) (net.Conn, error) {
		return client, nil
	}

	serverErr := make(chan error, 1)
	go func() {
		messageType, payload, err := readSwayMessage(server)
		if err != nil {
			serverErr <- err
			return
		}
		if messageType != swayMsgSubscribe ||
			string(payload) != `["window"]` {
			serverErr <- fmt.Errorf(
				"subscribe = type %d payload %q",
				messageType,
				payload,
			)
			return
		}
		if err := writeSwayMessage(
			server,
			swayMsgSubscribe,
			`{"success":true}`,
		); err != nil {
			serverErr <- err
			return
		}
		if err := writeSwayMessage(
			server,
			1<<31|3,
			`{"change":"new"}`,
		); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	manager := &SwayManager{sock: "test"}
	changes := manager.WindowChanges()
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("WindowChanges did not receive sway event")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("subscription server: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSwayWindowChangesRejectsUnexpectedSubscriptionResponse(t *testing.T) {
	originalDial := swayDialContext
	t.Cleanup(func() {
		swayDialContext = originalDial
	})

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
	})
	swayDialContext = func(
		context.Context,
		string,
		string,
	) (net.Conn, error) {
		return client, nil
	}

	serverErr := make(chan error, 1)
	go func() {
		messageType, _, err := readSwayMessage(server)
		if err != nil {
			serverErr <- err
			return
		}
		if messageType != swayMsgSubscribe {
			serverErr <- fmt.Errorf("subscribe request type = %d, want %d", messageType, swayMsgSubscribe)
			return
		}
		serverErr <- writeSwayMessage(server, swayMsgGetTree, `{"unexpected":true}`)
	}()

	manager := &SwayManager{sock: "test"}
	changes := manager.WindowChanges()
	select {
	case _, ok := <-changes:
		if ok {
			t.Fatal("WindowChanges emitted an event after an invalid subscription response")
		}
	case <-time.After(time.Second):
		t.Fatal("WindowChanges did not stop after an invalid subscription response")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("subscription server: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSwayWindowChangesCloseCancelsInitialDial(t *testing.T) {
	originalDial := swayDialContext
	t.Cleanup(func() {
		swayDialContext = originalDial
	})

	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	swayDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(dialStarted)
		if ctx.Done() == nil {
			<-releaseDial
			return nil, errors.New("dial released")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	manager := &SwayManager{sock: "test"}
	manager.WindowChanges()
	<-dialStarted

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- manager.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(releaseDial)
		t.Fatal("Close did not cancel the initial event-subscription dial")
	}
}

type cancelOnSwayAdmissionCheck struct {
	context.Context //nolint:containedctx // test cancels at the exact post-gate admission check.
	cancel          context.CancelFunc
	once            sync.Once
}

func (c *cancelOnSwayAdmissionCheck) Err() error {
	c.once.Do(c.cancel)
	return c.Context.Err()
}

// A newly available gate cannot authorize an operation whose context expires
// during admission. The returned token must still be restored for the next call.
func TestSwayGateRejectsCancellationAfterAcquiringToken(t *testing.T) {
	m := &SwayManager{}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelOnSwayAdmissionCheck{Context: base, cancel: cancel}
	release, err := m.admit(ctx)
	if release != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled gate admission leaked token or succeeded: release=%v err=%v", release != nil, err)
	}
	healthy, err := m.admit(context.Background())
	if err != nil || healthy == nil {
		t.Fatalf("canceled admission permanently consumed gate: %v", err)
	}
	healthy()
}

// Closing while a query waits on the gate must not permit a late transport
// dial, even when the gate is subsequently released.
func TestSwayGateWaiterRefusesClosedManagerWithoutDial(t *testing.T) {
	m := &SwayManager{sock: "unused"}
	hold, err := m.admit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.closed.Store(true)
	hold()
	_, err = m.query(context.Background(), swayMsgGetTree, "")
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed manager admitted a transport query: %v", err)
	}
}

// Only an actionable leaf container with a positive Sway identity can own
// the reported active-window title. Container-group and zero-ID focus flags
// are not a real window even when they carry a nonempty name.
func TestSwayActiveTitleSkipsNonWindowFocusedNodes(t *testing.T) {
	cases := []struct{ name, tree string }{
		{"zero identity", `{"id":1,"type":"root","nodes":[{"id":0,"type":"con","name":"phantom focus","focused":true}]}`},
		{"group container", `{"id":1,"type":"root","nodes":[{"id":22,"type":"con","name":"group title","focused":true,"nodes":[{"id":23,"type":"con","name":"child","focused":false}]}]}`},
		{"negative identity", `{"id":1,"type":"root","nodes":[{"id":-3,"type":"con","name":"phantom focus","focused":true}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, tc.tree, `[{"success":true}]`)
			title, err := m.ActiveTitle(context.Background())
			if title != "" || err == nil {
				t.Errorf("non-window focus reported: title=%q err=%v", title, err)
			}
			if _, err := finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A syntactically valid JSON body is not necessarily a Sway GET_TREE result.
// Treating JSON null or an object with no root identity as an empty desktop
// causes automation to falsely report that windows have disappeared.
func TestSwayWindowDiscoveryRejectsInvalidTreeRoot(t *testing.T) {
	for _, tc := range []struct{ name, tree string }{
		{"null", "null"},
		{"empty_object", "{}"},
		{"zero_id", `{"id":0,"type":"root"}`},
		{"wrong_type", `{"id":1,"type":"con"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, tc.tree, `[{"success":true}]`)
			windows, err := m.List(context.Background())
			if err == nil || !strings.Contains(err.Error(), "invalid tree root") || len(windows) != 0 {
				t.Errorf("invalid Sway root accepted: windows=%+v err=%v", windows, err)
			}
			if _, err := finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSwayWindowDiscoveryAcceptsGenuineEmptyRoot(t *testing.T) {
	m, finish := newSyntheticSwayCommands(t, `{"id":1,"type":"root","nodes":[]}`, `[{"success":true}]`)
	windows, err := m.List(context.Background())
	if err != nil || len(windows) != 0 {
		t.Errorf("valid empty desktop windows=%+v err=%v", windows, err)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}

type failingSwayConn struct {
	stubSwayConn
	deadlineErr error
	writeErr    error
	cancel      context.CancelFunc
}

func (c *failingSwayConn) SetDeadline(deadline time.Time) error {
	if err := c.stubSwayConn.SetDeadline(deadline); err != nil {
		return err
	}
	return c.deadlineErr
}
func (c *failingSwayConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writeCalls++
	c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(p), nil
}

// Failed admission to a transport deadline must not send any IPC request.
func TestSwayQueryRejectsDeadlineSetupFailureWithoutWrite(t *testing.T) {
	sentinel := errors.New("synthetic deadline rejected")
	conn := &failingSwayConn{deadlineErr: sentinel}
	data, err := swayQueryConnContext(context.Background(), conn, swayMsgGetTree, "private command")
	if len(data) != 0 || !errors.Is(err, sentinel) {
		t.Fatalf("unexpected result: %q %v", data, err)
	}
	_, deadlines, writes, _ := conn.snapshot()
	if deadlines != 1 || writes != 0 {
		t.Fatalf("failed deadline touched transport: deadlines=%d writes=%d", deadlines, writes)
	}
}

// Cancellation during the write takes priority over a transport-level failure;
// this keeps cancellation from being reported as an unrelated IPC error.
func TestSwayQueryPrefersCancellationOverConcurrentWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &failingSwayConn{cancel: cancel, writeErr: io.ErrClosedPipe}
	data, err := swayQueryConnContext(ctx, conn, swayMsgRunCommand, "sensitive")
	if len(data) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %q %v", data, err)
	}
	_, _, writes, _ := conn.snapshot()
	if writes != 1 {
		t.Fatalf("canceled send executed %d writes", writes)
	}
}

// The second query on the same manager must never reuse a connection after an
// invalid IPC response, even when the fake server remains connected.
func TestSwayManagerRetiresConnectionAfterMalformedResponse(t *testing.T) {
	original := swayDialContext
	t.Cleanup(func() { swayDialContext = original })
	firstClient, firstPeer := net.Pipe()
	secondClient, secondPeer := net.Pipe()
	t.Cleanup(func() {
		_ = firstClient.Close()
		_ = firstPeer.Close()
		_ = secondClient.Close()
		_ = secondPeer.Close()
	})
	dials := 0
	swayDialContext = func(context.Context, string, string) (net.Conn, error) {
		dials++
		if dials == 1 {
			return firstClient, nil
		}
		if dials == 2 {
			return secondClient, nil
		}
		return nil, errors.New("unexpected third dial")
	}
	serverResult := make(chan error, 2)
	go func() {
		_, _, err := readSwayMessage(firstPeer)
		if err == nil {
			err = writeSwayMessage(firstPeer, swayMsgSubscribe, `{ "success": true }`)
		}
		serverResult <- err
	}()
	go func() {
		_, _, err := readSwayMessage(secondPeer)
		if err == nil {
			err = writeSwayMessage(secondPeer, swayMsgGetTree, `{ "id": 1, "type": "root" }`)
		}
		serverResult <- err
	}()
	manager := &SwayManager{sock: "private"}
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := manager.query(ctx, swayMsgGetTree, ""); err == nil {
		t.Fatal("accepted wrong response type")
	}
	raw, err := manager.query(ctx, swayMsgGetTree, "")
	if err != nil || string(raw) != `{ "id": 1, "type": "root" }` {
		t.Fatalf("new connection not used: %q %v", raw, err)
	}
	if dials != 2 {
		t.Fatalf("failed response did not force fresh dial: %d", dials)
	}
	for i := 0; i < 2; i++ {
		if err := <-serverResult; err != nil {
			t.Fatal(err)
		}
	}
}

// A transport-level send failure must not be disguised as a successful
// query, and no read should follow a request that was never delivered.
func TestSwayQueryConnPreservesWriteFailureBeforeResponse(t *testing.T) {
	conn := &failingSwayConn{writeErr: io.ErrClosedPipe}
	body, err := swayQueryConn(context.Background(), conn, swayMsgGetTree, "private")
	if !errors.Is(err, io.ErrClosedPipe) || len(body) != 0 {
		t.Fatalf("write failure: body=%q err=%v", body, err)
	}
	_, deadlineCalls, writeCalls, _ := conn.snapshot()
	if deadlineCalls != 1 || writeCalls != 1 {
		t.Fatalf("attempts after failed send: deadlines=%d writes=%d", deadlineCalls, writeCalls)
	}
}

// Cancellation at the context-aware query entrypoint must not touch an IPC
// transport, even when an otherwise usable connection is supplied.
func TestSwayQueryConnContextRejectsPreCanceledRequestWithoutIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := &stubSwayConn{}
	body, err := swayQueryConnContext(ctx, conn, swayMsgGetTree, "private")
	if !errors.Is(err, context.Canceled) || len(body) != 0 {
		t.Fatalf("pre-canceled result: body=%q err=%v", body, err)
	}
	_, deadlineCalls, writeCalls, _ := conn.snapshot()
	if deadlineCalls != 0 || writeCalls != 0 {
		t.Fatalf("canceled request touched transport: deadlines=%d writes=%d", deadlineCalls, writeCalls)
	}
}

// Failed window mutations are failures even before a wire message can be
// sent, including after the compositor session has been closed.
func TestSwayCommandPropagatesClosedManagerFailure(t *testing.T) {
	m := &SwayManager{}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.swayCmd(context.Background(), "[con_id=17] focus"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed manager accepted focus command: %v", err)
	}
}

// Exercise response parsing through the actual request/response transport, not
// a fabricated parsed message: malformed and truncated compositor frames must
// fail closed rather than becoming successful window discovery or mutation.
func TestSwayIPCRejectsMalformedResponseWireFrames(t *testing.T) {
	for _, tc := range []struct {
		name      string
		makeReply func() (uint32, []byte)
		want      string
	}{
		{"valid tree response", func() (uint32, []byte) { return swayMsgGetTree, []byte(`{"id":1,"type":"root"}`) }, ""},
		{"wrong response type", func() (uint32, []byte) { return swayMsgSubscribe, []byte(`{"success":true}`) }, "unexpected response type"},
		{"bad magic", func() (uint32, []byte) {
			msg := swayFrame(swayMsgGetTree, []byte(`{}`))
			copy(msg[:6], []byte("BADIPC"))
			// Invalid magic is rejected after the header; sending an extra
			// payload on a synchronous pipe would block the fake peer.
			return ^uint32(0), msg[:14]
		}, "bad magic"},
		{"oversized advertised frame", func() (uint32, []byte) {
			msg := swayFrame(swayMsgGetTree, nil)
			binary.LittleEndian.PutUint32(msg[6:10], 64<<20+1)
			return ^uint32(0), msg
		}, "message body too large"},
		{"short header", func() (uint32, []byte) { return ^uint32(0), []byte("i3-ipc") }, "unexpected EOF"},
		{"truncated response payload", func() (uint32, []byte) {
			msg := swayFrame(swayMsgGetTree, []byte(`{"id":1}`))
			return ^uint32(0), msg[:len(msg)-2]
		}, "unexpected EOF"},
		{"unexpected command response", func() (uint32, []byte) { return swayMsgRunCommand, []byte(`[{"success":true}]`) }, "unexpected response type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, peer := net.Pipe()
			defer client.Close()
			defer peer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			serverErr := make(chan error, 1)
			go func() {
				defer peer.Close()
				typ, body, err := readSwayMessage(peer)
				if err != nil {
					serverErr <- err
					return
				}
				if typ != swayMsgGetTree || len(body) != 0 {
					serverErr <- fmt.Errorf("unexpected request type=%d body=%q", typ, body)
					return
				}
				kind, raw := tc.makeReply()
				if kind != ^uint32(0) {
					raw = swayFrame(kind, raw)
				}
				_, err = peer.Write(raw)
				serverErr <- err
			}()
			raw, err := swayQueryConnContext(ctx, client, swayMsgGetTree, "")
			if tc.want == "" {
				if err != nil || string(raw) != `{"id":1,"type":"root"}` {
					t.Fatalf("valid tree response got %q %v", raw, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) || len(raw) != 0 {
				t.Fatalf("malformed response accepted raw=%q error=%v, want %q", raw, err, tc.want)
			}
			if err := <-serverErr; err != nil && !errors.Is(err, net.ErrClosed) {
				t.Fatalf("synthetic compositor send: %v", err)
			}
		})
	}
}

func swayFrame(typ uint32, body []byte) []byte {
	out := make([]byte, 14+len(body))
	copy(out[:6], swayMagic[:])
	binary.LittleEndian.PutUint32(out[6:10], uint32(len(body)))
	binary.LittleEndian.PutUint32(out[10:14], typ)
	copy(out[14:], body)
	return out
}

func TestSwayTreeIteratorRejectsMalformedTreeAndStopsAtConsumerBoundary(t *testing.T) {
	m, finish := newSyntheticSwayCommands(t, "{no-json", `[{"success":true}]`)
	if got, err := m.List(context.Background()); err == nil || got != nil {
		t.Fatalf("malformed tree accepted: windows=%v error=%v", got, err)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
	m, finish = newSyntheticSwayCommands(t, `{"id":1,"type":"root","floating_nodes":[{"id":42,"type":"floating_con","name":"First"},{"id":43,"type":"floating_con","name":"Second"}]}`, `[{"success":true}]`)
	emitted := 0
	for info, err := range m.IterateWindows(context.Background()) {
		if err != nil {
			t.Fatal(err)
		}
		if info.Title != "First" {
			t.Fatalf("iterator emitted beyond consumer stop: %+v", info)
		}
		emitted++
		break
	}
	if emitted != 1 {
		t.Fatalf("iterator emitted %d windows", emitted)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}

func TestSwayIteratorDoesNotPublishLaterWindowsAfterCancellation(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"first"},{"id":43,"type":"con","name":"private-second"}]}`
	manager := &SwayManager{conn: newSuccessSwayConn([]byte(tree))}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	canceled := false
	for info, err := range manager.IterateWindows(ctx) {
		if errors.Is(err, context.Canceled) {
			if info.ID != 0 {
				t.Fatalf("cancellation yielded private window %#v", info)
			}
			canceled = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		if count != 1 || info.Title != "first" {
			t.Fatalf("window yielded after cancellation: %#v", info)
		}
		cancel()
	}
	if count != 1 || !canceled {
		t.Fatalf("yielded %d windows, cancellation=%v; want one window then cancellation", count, canceled)
	}
}

func TestSwayIteratorNilContextUsesDefaultContext(t *testing.T) {
	manager := &SwayManager{conn: newSuccessSwayConn([]byte(`{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"app"}]}`))}
	seen := 0
	for info, err := range manager.IterateWindows(nil) {
		if err != nil {
			t.Fatal(err)
		}
		if info.ID != 42 || info.Title != "app" {
			t.Fatalf("unexpected default context result: %+v", info)
		}
		seen++
	}
	if seen != 1 {
		t.Fatalf("got %d windows, want 1", seen)
	}
}

type cancelSwayContextAfterErrCheck struct {
	context.Context
	cancel      context.CancelFunc
	cancelAfter int
	checks      int
}

func (c *cancelSwayContextAfterErrCheck) Err() error {
	c.checks++
	err := c.Context.Err()
	if c.checks == c.cancelAfter {
		c.cancel()
	}
	return err
}

func TestSwayIteratorWithholdsTreeWhenCanceledAfterSuccessfulQuery(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"private-window"}]}`
	for _, tc := range []struct {
		name        string
		cancelAfter int
	}{
		{name: "after query response", cancelAfter: 6},
		{name: "before tree traversal", cancelAfter: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelSwayContextAfterErrCheck{Context: base, cancel: cancel, cancelAfter: tc.cancelAfter}
			manager := &SwayManager{conn: newSuccessSwayConn([]byte(tree))}

			var windows []Info
			var resultErr error
			for info, err := range manager.IterateWindows(ctx) {
				if err != nil {
					resultErr = err
					break
				}
				windows = append(windows, info)
			}
			if !errors.Is(resultErr, context.Canceled) || len(windows) != 0 {
				t.Fatalf("canceled tree query returned windows=%+v err=%v", windows, resultErr)
			}
			if ctx.checks != tc.cancelAfter+1 {
				t.Fatalf("cancellation check count=%d, want %d", ctx.checks, tc.cancelAfter+1)
			}
		})
	}
}

func TestSwayActiveTitleWithholdsTreeWhenCanceledAfterSuccessfulQuery(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelSwayContextAfterErrCheck{Context: base, cancel: cancel, cancelAfter: 6}
	tree := `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"private-title","focused":true}]}`
	manager := &SwayManager{conn: newSuccessSwayConn([]byte(tree))}

	title, err := manager.ActiveTitle(ctx)
	if title != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled active-title query returned title=%q err=%v", title, err)
	}
	if ctx.checks != 7 {
		t.Fatalf("cancellation check count=%d, want 7", ctx.checks)
	}
}

// Sway's move position command accepts signed 32-bit geometry. A value that
// cannot be represented by the compositor must not trigger the preceding
// "floating enable" mutation or even the discovery IPC request.
func TestSwayMoveRejectsUnrepresentableCoordinatesBeforeMutatingWindow(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"huge positive x", math.MaxInt, 0},
		{"huge positive y", 0, math.MaxInt},
		{"huge negative x", math.MinInt, 0},
		{"huge negative y", 0, math.MinInt},
		{"just above signed x", int(math.MaxInt32) + 1, 0},
		{"just below signed y", 0, int(math.MinInt32) - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
			err := m.MoveByID(context.Background(), "42", tc.x, tc.y)
			commands, serverErr := finish()
			if err == nil || serverErr != nil || len(commands) != 0 {
				t.Fatalf("unsupported coordinate move (%d,%d) dispatched partial IPC %q: err=%v server=%v", tc.x, tc.y, commands, err, serverErr)
			}
		})
	}
}

func TestSwayMoveAllowsSignedCoordinateBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"minimum signed x", int(math.MinInt32), -1},
		{"maximum signed y", 3, int(math.MaxInt32)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
			err := m.MoveByID(context.Background(), "42", tc.x, tc.y)
			commands, serverErr := finish()
			if err != nil || serverErr != nil || len(commands) != 3 {
				t.Fatalf("valid signed coordinate move (%d,%d) rejected: err=%v server=%v commands=%q", tc.x, tc.y, err, serverErr, commands)
			}
		})
	}
}

func TestSwayMutatorsDoNotSendCommandsForUnresolvedWindowIdentities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*SwayManager) error
	}{
		{"move", func(m *SwayManager) error { return m.MoveByID(context.Background(), "999", 20, 25) }},
		{"resize", func(m *SwayManager) error { return m.ResizeByID(context.Background(), "999", 480, 320) }},
		{"close", func(m *SwayManager) error { return m.CloseWindowByID(context.Background(), "999") }},
		{"minimize", func(m *SwayManager) error { return m.MinimizeByID(context.Background(), "999") }},
		{"fullscreen", func(m *SwayManager) error { return m.FullscreenByID(context.Background(), "999") }},
		{"unfullscreen", func(m *SwayManager) error { return m.UnfullscreenByID(context.Background(), "999") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
			err := tc.invoke(m)
			calls, serverErr := finish()
			if !errors.Is(err, ErrWindowNotFound) || serverErr != nil || !reflect.DeepEqual(calls, []string{"GET_TREE"}) {
				t.Fatalf("unresolved target operation: err=%v cmds=%q server=%v", err, calls, serverErr)
			}
		})
	}
}

func TestSwayCompoundMutationsStopAfterRejectedFloatingStep(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*SwayManager) error
	}{
		{"move", func(m *SwayManager) error { return m.MoveByID(context.Background(), "42", 20, 25) }},
		{"resize", func(m *SwayManager) error { return m.ResizeByID(context.Background(), "42", 480, 320) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":false,"error":"synthetic compositor rejected floating"}]`)
			err := tc.invoke(m)
			commands, serverErr := finish()
			expected := []string{"GET_TREE", "[con_id=42] floating enable"}
			if err == nil || serverErr != nil || !reflect.DeepEqual(commands, expected) {
				t.Fatalf("rejected first mutation continued operation: err=%v commands=%v server=%v", err, commands, serverErr)
			}
		})
	}
}

// Compositor geometry is advisory, but public widths and heights cannot be
// negative: unknown or malformed dimensions must be reported as zero rather
// than leaking an impossible rectangle to geometry-sensitive callers.
func TestSwayWindowDiscoveryClampsNegativeDimensionsToUnknown(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":41,"type":"con","name":"invalid width","rect":{"x":-10,"y":40,"width":-20,"height":300}},{"id":42,"type":"floating_con","name":"invalid height","rect":{"x":5,"y":-20,"width":100,"height":-1}},{"id":43,"type":"con","name":"valid","rect":{"x":10,"y":20,"width":640,"height":480}}]}`
	manager, finish := newSyntheticSwayCommands(t, tree, `[{"success":true}]`)
	windows, err := manager.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 3 {
		t.Fatalf("expected all 3 identities despite malformed geometry, got %+v", windows)
	}
	for _, tc := range []struct {
		id   string
		w, h int
	}{{"41", 0, 300}, {"42", 100, 0}, {"43", 640, 480}} {
		matched := false
		for _, win := range windows {
			if win.StableID() != tc.id {
				continue
			}
			matched = true
			if win.W != tc.w || win.H != tc.h {
				t.Errorf("window %s exported invalid geometry %dx%d, expected %dx%d", tc.id, win.W, win.H, tc.w, tc.h)
			}
		}
		if !matched {
			t.Errorf("window %s omitted", tc.id)
		}
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}

// A canceled query cannot inspect invalid window identities or return
// metadata; its lifetime must be checked before parsing caller input.
func TestSwayInfoByIDRejectsCanceledCallerBeforeParsingWindowID(t *testing.T) {
	manager := &SwayManager{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := manager.InfoByID(ctx, "not-an-id"); !errors.Is(err, context.Canceled) || got != (Info{}) {
		t.Fatalf("canceled lookup returned info=%+v error=%v", got, err)
	}
}

// A canceled caller no longer has authority to inspect target identifiers or
// validate mutation payloads, much less to dispatch compositor commands.
func TestSwayCanceledMutationsRejectBeforeIdentifierAndGeometryValidation(t *testing.T) {
	m := &SwayManager{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"activate invalid id", func(c context.Context) error { return m.ActivateByID(c, "not-a-window") }},
		{"move invalid coordinate", func(c context.Context) error { return m.MoveByID(c, "1", 1<<40, 0) }},
		{"resize invalid dimensions", func(c context.Context) error { return m.ResizeByID(c, "1", 0, -1) }},
		{"close invalid id", func(c context.Context) error { return m.CloseWindowByID(c, "not-a-window") }},
		{"minimize invalid id", func(c context.Context) error { return m.MinimizeByID(c, "not-a-window") }},
		{"fullscreen invalid id", func(c context.Context) error { return m.FullscreenByID(c, "not-a-window") }},
		{"unfullscreen invalid id", func(c context.Context) error { return m.UnfullscreenByID(c, "not-a-window") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled mutation exposed validation result: %v", err)
			}
		})
	}
}

// A socket that echoes the i3 IPC message type is not sufficient evidence of
// a real Sway compositor. Discovery must validate its GET_TREE body before
// granting a manager that can subsequently issue commands on that socket.
func TestSwayDiscoveryRejectsSocketWithInvalidTreeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"malformed JSON", "not-json", false},
		{"empty response", "", false},
		{"untyped JSON", `[]`, false},
		{"missing tree identity", `{}`, false},
		{"zero root ID", `{"id":0,"type":"root"}`, false},
		{"wrong root node type", `{"id":1,"type":"con"}`, false},
		{"plausible Sway root", `{"id":1,"type":"root","nodes":[]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := testutil.ShortSocketPath(t, "synthetic-ipc.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverErr := make(chan error, 1)
			go func() {
				conn, acceptErr := listener.Accept()
				if acceptErr != nil {
					serverErr <- acceptErr
					return
				}
				defer conn.Close()
				typ, body, readErr := readSwayMessage(conn)
				if readErr == nil && (typ != swayMsgGetTree || len(body) != 0) {
					readErr = errors.New("unexpected discovery request")
				}
				if readErr == nil {
					readErr = writeSwayMessage(conn, swayMsgGetTree, tc.body)
				}
				serverErr <- readErr
			}()
			manager, err := NewSwayManagerRuntimeContext(context.Background(), env.FromEnviron([]string{"SWAYSOCK=" + socket}))
			if tc.valid {
				if err != nil || manager == nil {
					t.Fatalf("valid compositor probe rejected: %+v %v", manager, err)
				}
				if closeErr := manager.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			} else if err == nil || manager != nil {
				t.Fatalf("unverified tree accepted as authoritative compositor: %+v %v", manager, err)
			}
			if err := <-serverErr; err != nil {
				t.Fatalf("synthetic compositor server failed: %v", err)
			}
		})
	}
}

// The peer can finish replying at the exact moment the caller loses its
// lifetime. A completed read must not publish that response after cancellation.
type swayCancelAfterBodyConn struct {
	net.Conn
	cancel context.CancelFunc
	reads  int
}

func (c *swayCancelAfterBodyConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.reads++
		if c.reads == 2 {
			c.cancel()
		}
	}
	return n, err
}
func TestSwayQueryNeverPublishesResponseCanceledDuringFinalRead(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		request := make([]byte, 14)
		if _, err := server.Read(request); err != nil {
			done <- err
			return
		}
		response := make([]byte, 16)
		copy(response[:6], swayMagic[:])
		binary.LittleEndian.PutUint32(response[6:10], 2)
		binary.LittleEndian.PutUint32(response[10:14], swayMsgGetTree)
		copy(response[14:], "{}")
		_, err := server.Write(response)
		done <- err
	}()
	body, err := swayQueryConn(ctx, &swayCancelAfterBodyConn{Conn: client, cancel: cancel}, swayMsgGetTree, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("response %q published with error %v after final read canceled", body, err)
	}
	if serverErr := <-done; serverErr != nil {
		t.Fatal(serverErr)
	}
}

func TestNewSwayManagerReturnsCancellationFromRuntimeSocketProbe(t *testing.T) {
	runtimeDir := testutil.ShortSocketDir(t)
	socket := filepath.Join(runtimeDir, "sway-ipc.test.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	dialEntered := make(chan struct{})
	releaseDial := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseDial) }) }
	originalDial := swayDialContext
	t.Cleanup(func() {
		release()
		swayDialContext = originalDial
	})
	swayDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(dialEntered)
		<-releaseDial
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		manager *SwayManager
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		manager, openErr := NewSwayManagerRuntimeContext(ctx, env.FromEnviron([]string{"XDG_RUNTIME_DIR=" + runtimeDir}))
		resultCh <- result{manager: manager, err: openErr}
	}()

	select {
	case <-dialEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime discovery did not reach the advertised Sway socket")
	}
	cancel()
	release()

	select {
	case got := <-resultCh:
		if got.manager != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("runtime socket probe returned manager=%v err=%v, want context.Canceled", got.manager, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime discovery did not return after cancellation")
	}
}

func TestSwayCloseDuringSubscriptionDialClosesLateConnection(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	dialEntered := make(chan struct{})
	releaseDial := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseDial) }) }
	originalDial := swayDialContext
	swayDialContext = func(context.Context, string, string) (net.Conn, error) {
		close(dialEntered)
		<-releaseDial
		return client, nil
	}

	manager := &SwayManager{sock: "synthetic"}
	t.Cleanup(func() {
		release()
		_ = manager.Close()
		swayDialContext = originalDial
	})
	events := manager.WindowChanges()
	select {
	case <-dialEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("Sway subscription did not begin dialing")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close() }()
	select {
	case <-manager.eventStop:
	case <-time.After(2 * time.Second):
		t.Fatal("manager close did not signal the pending subscription")
	}
	release()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("closing manager during subscription dial: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manager close did not join the pending subscription")
	}
	if _, ok := <-events; ok {
		t.Fatal("closed manager left its event stream open")
	}
	var one [1]byte
	if _, err := peer.Read(one[:]); err == nil {
		t.Fatal("late subscription peer remained open after manager close")
	}
}

// An explicitly configured runtime path is not silently replaced with the
// process runtime directory, and missing folders must be diagnostic errors.
func TestSwayRuntimeDiscoveryRejectsMissingRuntimeDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-sway-runtime")
	manager, err := NewSwayManagerRuntimeContext(context.Background(), env.FromEnviron([]string{"SWAYSOCK=", "XDG_RUNTIME_DIR=" + missing}))
	if manager != nil || err == nil || !strings.Contains(err.Error(), "inspect runtime sockets") || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit runtime directory unexpectedly accepted: manager=%+v error=%v", manager, err)
	}
}

// A fake Sway socket can be a normal file, directory or a socket-looking
// symlink. None is a valid direct Unix socket entry for compositor probing.
func TestSwayRuntimeDiscoverySkipsNonSocketCandidateEntries(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"other.sock", "sway-ipc..sock", "sway-ipc.12.txt", "sway-ipc.42.sock"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sway-ipc.43.sock"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "sway-ipc.42.sock"), filepath.Join(dir, "sway-ipc.44.sock")); err != nil {
		t.Fatal(err)
	}
	manager, err := NewSwayManagerRuntimeContext(context.Background(), env.FromEnviron([]string{"SWAYSOCK=", "XDG_RUNTIME_DIR=" + dir}))
	if manager != nil || err == nil || !strings.Contains(err.Error(), "no reachable sway IPC socket") {
		t.Fatalf("non-socket entries falsely authorized compositor: manager=%+v err=%v", manager, err)
	}
}

// The runtime directory may contain characters meaningful to glob syntax.
// Runtime fallback must discover the actual socket instead of skipping it.
func TestSwayRuntimeDiscoveryTreatsRuntimeDirectoryAsLiteral(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pfsway-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "private[unfinished")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "sway-ipc.1.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_, _, err = readSwayMessage(conn)
		if err == nil {
			err = writeSwayMessage(conn, swayMsgGetTree, `{"id":1,"type":"root"}`)
		}
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	manager, err := NewSwayManagerRuntimeContext(ctx, env.FromEnviron([]string{"XDG_RUNTIME_DIR=" + dir}))
	if err != nil {
		t.Fatalf("valid socket inside literal runtime %q not discovered: %v", dir, err)
	}
	defer manager.Close()
	if manager.sock != socket {
		t.Fatalf("discovered socket %q instead of %q", manager.sock, socket)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Runtime discovery must reject a Sway socket that accepts a query but closes
// before returning the compositor tree.
func TestSwayRuntimeProbePropagatesDisconnectedTreeResponse(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "sway-ipc.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		msgType, _, readErr := readSwayMessage(conn)
		if readErr == nil && msgType != swayMsgGetTree {
			readErr = errors.New("runtime probe sent a non-tree request")
		}
		_ = conn.Close()
		serverDone <- readErr
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = swayQueryOnceContext(ctx, socket, swayMsgGetTree, "")
	if !errors.Is(err, io.EOF) {
		t.Fatalf("runtime probe error = %v, want disconnected tree response", err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic Sway server: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic Sway server did not receive the tree query: %v", ctx.Err())
	}
}

// The first Sway subscribe reply must explicitly confirm success. An IPC
// socket that rejects subscription is not a usable event source, even if it
// sends later unrelated messages; the channel should terminate promptly.
func TestSwaySubscriptionRejectsDeniedOrMalformedAcks(t *testing.T) {
	for _, ack := range []string{`{"success":false}`, `{}`, `{"success":"true"}`, `not-json`} {
		t.Run(ack, func(t *testing.T) {
			original := swayDialContext
			t.Cleanup(func() { swayDialContext = original })
			client, server := net.Pipe()
			defer server.Close()
			swayDialContext = func(context.Context, string, string) (net.Conn, error) { return client, nil }
			done := make(chan struct{})
			go func() {
				defer close(done)
				if _, _, err := readSwayMessage(server); err != nil {
					return
				}
				_ = writeSwayMessage(server, swayMsgSubscribe, ack)
			}()
			m := &SwayManager{sock: "synthetic"}
			t.Cleanup(func() { _ = m.Close() })
			events := m.WindowChanges()
			select {
			case _, ok := <-events:
				if ok {
					t.Fatal("rejected subscription emitted event")
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("denied or malformed subscribe reply left event stream open")
			}
			<-done
		})
	}
}

// Sway multiplexes IPC notifications using the top bit and distinct event
// opcodes. Workspace and output events cannot satisfy a window change watch.
func TestSwayWindowChangesIgnoresUnrelatedIPCEvents(t *testing.T) {
	original := swayDialContext
	t.Cleanup(func() { swayDialContext = original })
	client, server := net.Pipe()
	defer server.Close()
	swayDialContext = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	workspaceSent := make(chan struct{})
	sendWindow := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		if _, _, err := readSwayMessage(server); err != nil {
			serverDone <- err
			return
		}
		if err := writeSwayMessage(server, swayMsgSubscribe, `{"success":true}`); err != nil {
			serverDone <- err
			return
		}
		if err := writeSwayMessage(server, 1<<31|0, `{"change":"workspace"}`); err != nil {
			serverDone <- err
			return
		}
		close(workspaceSent)
		<-sendWindow
		serverDone <- writeSwayMessage(server, 1<<31|3, `{"change":"focus"}`)
	}()
	m := &SwayManager{sock: "synthetic"}
	events := m.WindowChanges()
	defer m.Close()
	<-workspaceSent
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("workspace event falsely reported as window change")
		}
		t.Fatal("subscription closed on unrelated event")
	case <-time.After(35 * time.Millisecond):
	}
	close(sendWindow)
	select {
	case _, ok := <-events:
		if !ok {
			t.Fatal("real window change lost")
		}
	case <-time.After(time.Second):
		t.Fatal("window change not delivered")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

type swaySubscriptionFaultConn struct {
	net.Conn
	deadlineCalls atomic.Int32
	failDeadline  int32
	writeErr      error
}

func (c *swaySubscriptionFaultConn) SetDeadline(deadline time.Time) error {
	if c.deadlineCalls.Add(1) == c.failDeadline {
		return errors.New("synthetic subscription deadline failure")
	}
	return c.Conn.SetDeadline(deadline)
}

func (c *swaySubscriptionFaultConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return c.Conn.Write(p)
}

func awaitSwaySubscriptionStop(t *testing.T, manager *SwayManager) {
	t.Helper()
	select {
	case <-manager.eventDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Sway subscription did not terminate after transport failure")
	}
}

func TestSwaySubscriptionTransportFailuresAndEventCoalescing(t *testing.T) {
	for _, tc := range []struct {
		name         string
		failDeadline int32
		writeErr     error
		acknowledge  bool
	}{
		{name: "initial deadline failure", failDeadline: 1},
		{name: "subscribe write failure", writeErr: errors.New("synthetic subscribe write failure")},
		{name: "deadline reset failure", failDeadline: 2, acknowledge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalDial := swayDialContext
			t.Cleanup(func() { swayDialContext = originalDial })
			client, server := net.Pipe()
			t.Cleanup(func() { _ = server.Close() })
			faultConn := &swaySubscriptionFaultConn{
				Conn:         client,
				failDeadline: tc.failDeadline,
				writeErr:     tc.writeErr,
			}
			swayDialContext = func(context.Context, string, string) (net.Conn, error) {
				return faultConn, nil
			}

			manager := &SwayManager{sock: "synthetic"}
			events := manager.WindowChanges()
			t.Cleanup(func() { _ = manager.Close() })
			serverDone := make(chan error, 1)
			if tc.acknowledge {
				go func() {
					if _, _, err := readSwayMessage(server); err != nil {
						serverDone <- err
						return
					}
					serverDone <- writeSwayMessage(server, swayMsgSubscribe, `{"success":true}`)
				}()
			}
			awaitSwaySubscriptionStop(t, manager)
			select {
			case _, ok := <-events:
				if ok {
					t.Fatal("failed Sway subscription published an event")
				}
			default:
				t.Fatal("failed Sway subscription left its event stream open")
			}
			if tc.acknowledge {
				if err := <-serverDone; err != nil {
					t.Fatalf("synthetic Sway peer: %v", err)
				}
			}
		})
	}

	t.Run("pending window changes coalesce", func(t *testing.T) {
		originalDial := swayDialContext
		t.Cleanup(func() { swayDialContext = originalDial })
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		swayDialContext = func(context.Context, string, string) (net.Conn, error) {
			return client, nil
		}

		manager := &SwayManager{sock: "synthetic"}
		events := manager.WindowChanges()
		t.Cleanup(func() { _ = manager.Close() })
		serverDone := make(chan error, 1)
		go func() {
			if _, _, err := readSwayMessage(server); err != nil {
				serverDone <- err
				return
			}
			if err := writeSwayMessage(server, swayMsgSubscribe, `{"success":true}`); err != nil {
				serverDone <- err
				return
			}
			for range 4 {
				if err := writeSwayMessage(server, 1<<31|3, `{"change":"focus"}`); err != nil {
					serverDone <- err
					return
				}
			}
			_ = server.Close()
			serverDone <- nil
		}()

		awaitSwaySubscriptionStop(t, manager)
		if err := <-serverDone; err != nil {
			t.Fatalf("synthetic Sway peer: %v", err)
		}
		count := 0
		for range events {
			count++
		}
		if count != 1 {
			t.Fatalf("pending window changes were not coalesced: received %d hints", count)
		}
	})
}

// A live Sway manager can acknowledge its local sync gate, while a retired
// manager cannot claim a usable input/window IPC session.
func TestSwaySyncDistinguishesLiveClosedAndNilManager(t *testing.T) {
	var live SwayManager
	if err := live.Sync(context.Background()); err != nil {
		t.Fatalf("live sync failed: %v", err)
	}
	live.closed.Store(true)
	if err := live.Sync(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed Sway manager acknowledged sync: %v", err)
	}
	var missing *SwayManager
	if err := missing.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "manager is nil") {
		t.Fatalf("missing Sway manager acknowledged sync: %v", err)
	}
}

// A completed KWin callback must provide the exact compositor payload, not
// substitute an empty or stale result.
func TestKWinCallbackReturnsVerifiedCompositorPayload(t *testing.T) {
	receiver := &pfReceiver{ch: make(chan string, 1)}
	const payload = `[{"id":"34","title":"Editor"}]`
	receiver.ch <- payload
	result, err := waitForKWinResult(context.Background(), receiver, 34)
	if err != nil || result != payload {
		t.Fatalf("valid callback result=%q err=%v", result, err)
	}
}

// Sway IPC window handles are positive. Zero is not a usable window identity,
// so a malformed leaf must never be advertised for handle-based actions.
func TestSwayTreeDiscoveryExcludesZeroAndNegativeWindowHandles(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":0,"type":"con","name":"zero-handle","rect":{"width":100,"height":100}},{"id":-1,"type":"con","name":"negative-handle","rect":{"width":100,"height":100}},{"id":42,"type":"con","name":"valid","rect":{"width":100,"height":100}}]}`
	manager, finish := newSyntheticSwayCommands(t, tree, `[{"success":true}]`)
	windows, err := manager.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 || windows[0].StableID() != "42" || windows[0].Title != "valid" {
		t.Fatalf("unusable Sway handles advertised: %+v", windows)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}
