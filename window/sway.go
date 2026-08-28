package window

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/env"
)

// swayMagic is the fixed 6-byte header prefix for all sway IPC messages.
var swayMagic = [6]byte{'i', '3', '-', 'i', 'p', 'c'}

const (
	swayMsgRunCommand = 0
	swayMsgSubscribe  = 2
	swayMsgGetTree    = 4
)

// swayNode is the recursive JSON tree node returned by GET_TREE.
type swayNode struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	AppID         string     `json:"app_id"`
	Type          string     `json:"type"`
	Rect          swayRect   `json:"rect"`
	Focused       bool       `json:"focused"`
	Nodes         []swayNode `json:"nodes"`
	FloatingNodes []swayNode `json:"floating_nodes"`
}

type swayRect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"width"`
	H int `json:"height"`
}

var _ Manager = (*SwayManager)(nil)

const defaultReflowTimeout = 500 * time.Millisecond

// SwayManager implements Manager via sway's IPC socket (i3-ipc protocol).
// It does not require any Wayland protocol machinery — it uses a simple
// Unix socket with length-prefixed JSON messages.
type SwayManager struct {
	sock string
	// mu protects conn.
	mu   sync.Mutex
	conn net.Conn

	eventOnce      sync.Once
	eventMu        sync.Mutex
	eventConn      net.Conn
	eventCh        chan struct{}
	eventStop      chan struct{}
	eventDone      chan struct{}
	closed         atomic.Bool
	eventCloseOnce sync.Once

	// ReflowTimeout controls how long Move waits for float layout reflow
	// after enabling floating on a tiled window. Zero means the default (500ms).
	ReflowTimeout time.Duration
}

// NewSwayManagerRuntime returns a SwayManager for the sway IPC environment in rt.
func NewSwayManagerRuntime(rt env.Runtime) (*SwayManager, error) {
	sock := rt.Get("SWAYSOCK")
	if sock != "" {
		if _, err := swayQueryOnce(sock, swayMsgGetTree, ""); err == nil {
			return &SwayManager{sock: sock}, nil
		}
	}
	rdir := rt.Get("XDG_RUNTIME_DIR")
	if rdir == "" {
		return nil, fmt.Errorf("window/sway: SWAYSOCK not set and XDG_RUNTIME_DIR empty")
	}
	matches, err := filepath.Glob(filepath.Join(rdir, "sway-ipc.*.sock"))
	if err != nil {
		return nil, fmt.Errorf("window/sway: glob sway sockets: %w", err)
	}
	for _, m := range matches {
		if _, err := swayQueryOnce(m, swayMsgGetTree, ""); err == nil {
			return &SwayManager{sock: m}, nil
		}
	}
	return nil, fmt.Errorf("window/sway: no reachable sway IPC socket found (set SWAYSOCK or start sway)")
}

func swayQueryDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(5 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		return ctxDeadline
	}
	return deadline
}

func swayQueryConn(ctx context.Context, conn net.Conn, msgType uint32, payload string) ([]byte, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(swayQueryDeadline(ctx)); err != nil {
		return nil, err
	}

	if err := writeSwayMessage(conn, msgType, payload); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	body, err := readSwayResponse(conn, msgType)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	return body, nil
}

func writeSwayMessage(conn net.Conn, msgType uint32, payload string) error {
	pb := []byte(payload)
	msg := make([]byte, 14+len(pb))
	copy(msg[0:6], swayMagic[:])
	binary.LittleEndian.PutUint32(msg[6:10], uint32(len(pb)))
	binary.LittleEndian.PutUint32(msg[10:14], msgType)
	copy(msg[14:], pb)
	n, err := conn.Write(msg)
	if err != nil {
		return err
	}
	if n != len(msg) {
		return io.ErrShortWrite
	}
	return nil
}

func readSwayMessage(conn net.Conn) (uint32, []byte, error) {
	hdr := make([]byte, 14)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return 0, nil, err
	}
	if string(hdr[0:6]) != string(swayMagic[:]) {
		return 0, nil, fmt.Errorf("bad magic")
	}
	bodyLen := binary.LittleEndian.Uint32(hdr[6:10])
	if bodyLen > 64<<20 {
		return 0, nil, fmt.Errorf("message body too large: %d", bodyLen)
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return 0, nil, err
	}
	return binary.LittleEndian.Uint32(hdr[10:14]), body, nil
}

func readSwayResponse(conn net.Conn, expectedType uint32) ([]byte, error) {
	messageType, body, err := readSwayMessage(conn)
	if err != nil {
		return nil, err
	}
	if messageType != expectedType {
		return nil, fmt.Errorf("unexpected response type %d (want %d)", messageType, expectedType)
	}
	return body, nil
}

func (m *SwayManager) query(ctx context.Context, msgType uint32, payload string) ([]byte, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.closed.Load() {
		return nil, fmt.Errorf("window/sway: manager is closed: %w", net.ErrClosed)
	}
	if ctx.Done() != nil {
		return swayQueryOnceContext(ctx, m.sock, msgType, payload)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return nil, fmt.Errorf("window/sway: manager is closed: %w", net.ErrClosed)
	}

	if m.conn == nil {
		conn, err := swayDialContext(ctx, "unix", m.sock)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, err
		}
		m.conn = conn
	}

	body, err := swayQueryConn(ctx, m.conn, msgType, payload)
	if err != nil {
		_ = m.conn.Close()
		m.conn = nil
		return nil, err
	}
	return body, nil
}

// List returns all visible windows (leaf containers) in the sway tree.
func (m *SwayManager) List(ctx context.Context) ([]Info, error) {
	var out []Info
	for win, err := range m.IterateWindows(ctx) {
		if err != nil {
			return nil, err
		}
		out = append(out, win)
	}
	return out, nil
}

// IterateWindows returns an iterator over all visible windows in the sway tree.
func (m *SwayManager) IterateWindows(ctx context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		raw, err := m.query(ctx, swayMsgGetTree, "")
		if err != nil {
			yield(Info{}, fmt.Errorf("window/sway: get_tree: %w", err))
			return
		}
		var root swayNode
		if err := json.Unmarshal(raw, &root); err != nil {
			yield(Info{}, fmt.Errorf("window/sway: parse tree: %w", err))
			return
		}

		_ = walkTree(&root, func(n *swayNode) bool {
			isLeaf := len(n.Nodes) == 0 && len(n.FloatingNodes) == 0
			if isLeaf && (n.Type == "con" || n.Type == "floating_con") && n.Name != "" {
				info := Info{
					ID:       uint64(n.ID),
					NativeID: strconv.FormatInt(n.ID, 10),
					Title:    n.Name,
					AppID:    n.AppID,
					X:        n.Rect.X,
					Y:        n.Rect.Y,
					W:        n.Rect.W,
					H:        n.Rect.H,
				}
				if !yield(info, nil) {
					return false
				}
			}
			return true
		})
	}
}

// walkTree performs a pre-order traversal of the sway tree and calls fn for each node.
// If fn returns false, traversal stops.
func walkTree(n *swayNode, fn func(*swayNode) bool) bool {
	if !fn(n) {
		return false
	}
	for i := range n.Nodes {
		if !walkTree(&n.Nodes[i], fn) {
			return false
		}
	}
	for i := range n.FloatingNodes {
		if !walkTree(&n.FloatingNodes[i], fn) {
			return false
		}
	}
	return true
}

// ActiveTitle returns the title of the currently focused window.
func (m *SwayManager) ActiveTitle(ctx context.Context) (string, error) {
	raw, err := m.query(ctx, swayMsgGetTree, "")
	if err != nil {
		return "", fmt.Errorf("window/sway: get_tree: %w", err)
	}
	var root swayNode
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("window/sway: parse tree: %w", err)
	}
	var title string
	walkTree(&root, func(n *swayNode) bool {
		if n.Focused && (n.Type == "con" || n.Type == "floating_con") {
			title = n.Name
			return false
		}
		return true
	})
	if title == "" {
		return "", fmt.Errorf("window/sway: no focused window")
	}
	return title, nil
}

// swayCmd runs a sway IPC command and returns an error if sway reports failure.
func (m *SwayManager) swayCmd(ctx context.Context, cmd string) error {
	resp, err := m.query(ctx, swayMsgRunCommand, cmd)
	if err != nil {
		return err
	}
	var results []struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(resp, &results); err != nil {
		return fmt.Errorf("window/sway: decode response: %w", err)
	}
	if len(results) == 0 {
		return fmt.Errorf("window/sway: command returned no command result")
	}
	for _, result := range results {
		if !result.Success {
			return fmt.Errorf("window/sway: command failed: %s", result.Error)
		}
	}
	return nil
}

// Close releases the persistent IPC connection.
func (m *SwayManager) Close() error {
	m.closed.Store(true)
	m.mu.Lock()
	var queryErr error
	if m.conn != nil {
		queryErr = m.conn.Close()
		m.conn = nil
	}
	m.mu.Unlock()

	m.eventMu.Lock()
	m.eventCloseOnce.Do(func() {
		if m.eventStop != nil {
			close(m.eventStop)
		}
	})
	if m.eventConn != nil {
		_ = m.eventConn.Close()
		m.eventConn = nil
	}
	eventDone := m.eventDone
	m.eventMu.Unlock()
	if eventDone != nil {
		<-eventDone
	}
	return queryErr
}

// Sync verifies that the Sway IPC connection is usable.
func (m *SwayManager) Sync(ctx context.Context) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/sway: sync canceled: %w", err)
	}
	if m.closed.Load() {
		return fmt.Errorf("window/sway: manager is closed: %w", net.ErrClosed)
	}
	return nil
}

// SupportedOperations returns operations supported by Sway IPC.
func (m *SwayManager) SupportedOperations() []string {
	return []string{
		"discover",
		"info",
		"active-title",
		"activate",
		"move",
		"resize",
		"close",
		"minimize",
		"maximize",
		"fullscreen",
	}
}

// WindowChanges returns coalesced hints from a dedicated sway IPC
// subscription. Callers must still re-read authoritative state after a hint.
func (m *SwayManager) WindowChanges() <-chan struct{} {
	m.eventOnce.Do(func() {
		m.eventCh = make(chan struct{}, 1)
		m.eventStop = make(chan struct{})
		m.eventDone = make(chan struct{})

		if m.closed.Load() {
			close(m.eventCh)
			close(m.eventDone)
			return
		}
		go m.runWindowSubscription(m.eventStop)
	})
	return m.eventCh
}

func (m *SwayManager) runWindowSubscription(stop <-chan struct{}) {
	defer close(m.eventDone)
	defer close(m.eventCh)

	conn, err := swayDialContext(context.Background(), "unix", m.sock)
	if err != nil {
		return
	}
	m.eventMu.Lock()
	if m.closed.Load() {
		m.eventMu.Unlock()
		_ = conn.Close()
		return
	}
	m.eventConn = conn
	m.eventMu.Unlock()
	defer func() {
		_ = conn.Close()
		m.eventMu.Lock()
		if m.eventConn == conn {
			m.eventConn = nil
		}
		m.eventMu.Unlock()
	}()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return
	}
	if err := writeSwayMessage(conn, swayMsgSubscribe, `["window"]`); err != nil {
		return
	}
	if _, err := readSwayResponse(conn, swayMsgSubscribe); err != nil {
		return
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return
	}

	for {
		if _, _, err := readSwayMessage(conn); err != nil {
			return
		}
		select {
		case m.eventCh <- struct{}{}:
		default:
		}
		select {
		case <-stop:
			return
		default:
		}
	}
}

// --- Handle-based operations ---

func (m *SwayManager) findByID(
	ctx context.Context,
	id string,
) (uint64, error) {
	numeric, err := numericID(id)
	if err != nil {
		return 0, err
	}
	// Verify the window exists by iterating the tree.
	_, err = FindByID(ctx, m, numeric)
	return numeric, err
}

// ActivateByID focuses the window identified by id.
func (m *SwayManager) ActivateByID(ctx context.Context, id string) error {
	numeric, err := m.findByID(ctx, id)
	if err != nil {
		return err
	}
	return m.swayCmd(ctx, fmt.Sprintf("[con_id=%d] focus", int64(numeric)))
}

// MoveByID positions the window identified by id.
func (m *SwayManager) MoveByID(ctx context.Context, id string, x, y int) error {
	ctx = contextutil.Default(ctx)
	numeric, err := m.findByID(ctx, id)
	if err != nil {
		return err
	}
	if err := m.swayCmd(ctx, fmt.Sprintf("[con_id=%d] floating enable", int64(numeric))); err != nil {
		return err
	}
	reflowTimeout := m.ReflowTimeout
	if reflowTimeout <= 0 {
		reflowTimeout = defaultReflowTimeout
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(reflowTimeout)
loop:
	for {
		wins, err := m.List(ctx)
		if err != nil {
			return err
		}
		for _, win := range wins {
			if win.ID == numeric {
				break loop
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			break loop
		case <-ticker.C:
		}
	}
	return m.swayCmd(ctx, "[con_id="+strconv.FormatInt(int64(numeric), 10)+"] move position "+strconv.Itoa(x)+" "+strconv.Itoa(y))
}

// ResizeByID resizes the window identified by id.
func (m *SwayManager) ResizeByID(ctx context.Context, id string, width, height int) error {
	numeric, err := m.findByID(ctx, id)
	if err != nil {
		return err
	}
	if err := m.swayCmd(ctx, fmt.Sprintf("[con_id=%d] floating enable", int64(numeric))); err != nil {
		return err
	}
	return m.swayCmd(ctx, "[con_id="+strconv.FormatInt(int64(numeric), 10)+"] resize set "+strconv.Itoa(width)+" "+strconv.Itoa(height))
}

// CloseWindowByID closes the window identified by id.
func (m *SwayManager) CloseWindowByID(ctx context.Context, id string) error {
	numeric, err := m.findByID(ctx, id)
	if err != nil {
		return err
	}
	return m.swayCmd(ctx, fmt.Sprintf("[con_id=%d] kill", int64(numeric)))
}

// MinimizeByID moves the window identified by id to the scratchpad.
func (m *SwayManager) MinimizeByID(ctx context.Context, id string) error {
	numeric, err := m.findByID(ctx, id)
	if err != nil {
		return err
	}
	return m.swayCmd(ctx, fmt.Sprintf("[con_id=%d] move scratchpad", int64(numeric)))
}

// MaximizeByID enables fullscreen mode for the window identified by id.
func (m *SwayManager) MaximizeByID(ctx context.Context, id string) error {
	numeric, err := m.findByID(ctx, id)
	if err != nil {
		return err
	}
	return m.swayCmd(ctx, fmt.Sprintf("[con_id=%d] fullscreen enable", int64(numeric)))
}

// FullscreenByID enables fullscreen mode for the window identified by id.
func (m *SwayManager) FullscreenByID(ctx context.Context, id string) error {
	numeric, err := m.findByID(ctx, id)
	if err != nil {
		return err
	}
	return m.swayCmd(ctx, fmt.Sprintf("[con_id=%d] fullscreen enable", int64(numeric)))
}

// UnfullscreenByID disables fullscreen mode for the window identified by id.
func (m *SwayManager) UnfullscreenByID(ctx context.Context, id string) error {
	numeric, err := m.findByID(ctx, id)
	if err != nil {
		return err
	}
	return m.swayCmd(ctx, fmt.Sprintf("[con_id=%d] fullscreen disable", int64(numeric)))
}

// RestoreByID reports that Sway does not expose a separate restore operation.
func (m *SwayManager) RestoreByID(_ context.Context, _ string) error {
	return ErrNotSupported
}

// InfoByID returns fresh information for the window identified by id.
func (m *SwayManager) InfoByID(ctx context.Context, id string) (Info, error) {
	numeric, err := numericID(id)
	if err != nil {
		return Info{}, err
	}
	return FindByID(ctx, m, numeric)
}

// swayQueryOnce sends a single IPC request and returns the raw JSON response.
func swayQueryOnce(sock string, msgType uint32, payload string) ([]byte, error) {
	return swayQueryOnceContext(context.Background(), sock, msgType, payload)
}

var swayDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	return dialer.DialContext(ctx, network, address)
}

func swayQueryOnceContext(ctx context.Context, sock string, msgType uint32, payload string) ([]byte, error) {
	ctx = contextutil.Default(ctx)
	conn, err := swayDialContext(ctx, "unix", sock)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	defer conn.Close()

	type queryResult struct {
		data []byte
		err  error
	}

	ch := make(chan queryResult, 1)
	go func() {
		data, err := swayQueryConn(ctx, conn, msgType, payload)
		ch <- queryResult{data, err}
	}()

	select {
	case <-ctx.Done():
		_ = conn.SetDeadline(time.Now()) // best-effort cancel on context deadline
		<-ch
		return nil, ctx.Err()
	case r := <-ch:
		return r.data, r.err
	}
}
