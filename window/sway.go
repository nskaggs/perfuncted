package window

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

// swayMagic is the fixed 6-byte header prefix for all sway IPC messages.
var swayMagic = [6]byte{'i', '3', '-', 'i', 'p', 'c'}

const (
	swayMsgRunCommand = 0
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

// SwayManager implements Manager via sway's IPC socket (i3-ipc protocol).
// It does not require any Wayland protocol machinery — it uses a simple
// Unix socket with length-prefixed JSON messages.
type SwayManager struct {
	sock string
	mu   sync.Mutex
	conn net.Conn
}

// NewSwayManager returns a SwayManager connected to the nearest sway IPC
// socket. It checks $SWAYSOCK first, then globs all sway-ipc sockets in
// $XDG_RUNTIME_DIR and tries each until one responds.
func NewSwayManager() (*SwayManager, error) {
	return NewSwayManagerRuntime(env.Current())
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

func (m *SwayManager) query(msgType uint32, payload string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.conn == nil {
		conn, err := net.DialTimeout("unix", m.sock, 5*time.Second)
		if err != nil {
			return nil, err
		}
		m.conn = conn
	}

	// Set a deadline for this specific operation.
	m.conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Write: magic(6) + length(4 LE) + type(4 LE) + payload
	pb := []byte(payload)
	msg := make([]byte, 14+len(pb))
	copy(msg[0:6], swayMagic[:])
	binary.LittleEndian.PutUint32(msg[6:10], uint32(len(pb)))
	binary.LittleEndian.PutUint32(msg[10:14], msgType)
	copy(msg[14:], pb)

	if _, err := m.conn.Write(msg); err != nil {
		m.conn.Close()
		m.conn = nil
		return nil, err
	}

	// Read header: magic(6) + length(4 LE) + type(4 LE)
	hdr := make([]byte, 14)
	if _, err := io.ReadFull(m.conn, hdr); err != nil {
		m.conn.Close()
		m.conn = nil
		return nil, err
	}
	if string(hdr[0:6]) != string(swayMagic[:]) {
		m.conn.Close()
		m.conn = nil
		return nil, fmt.Errorf("bad magic")
	}
	bodyLen := binary.LittleEndian.Uint32(hdr[6:10])
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(m.conn, body); err != nil {
		m.conn.Close()
		m.conn = nil
		return nil, err
	}
	return body, nil
}

// List returns all visible windows (leaf containers) in the sway tree.
func (m *SwayManager) List(ctx context.Context) ([]Info, error) {
	raw, err := m.query(swayMsgGetTree, "")
	if err != nil {
		return nil, fmt.Errorf("window/sway: get_tree: %w", err)
	}
	var root swayNode
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("window/sway: parse tree: %w", err)
	}
	var out []Info
	collectLeaves(&root, &out)
	return out, nil
}

// collectLeaves walks the sway tree and appends leaf containers (real windows).
func collectLeaves(n *swayNode, out *[]Info) {
	isLeaf := len(n.Nodes) == 0 && len(n.FloatingNodes) == 0
	if isLeaf && (n.Type == "con" || n.Type == "floating_con") && n.Name != "" {
		*out = append(*out, Info{
			ID:    uint64(n.ID),
			Title: n.Name,
			X:     n.Rect.X,
			Y:     n.Rect.Y,
			W:     n.Rect.W,
			H:     n.Rect.H,
		})
	}
	for i := range n.Nodes {
		collectLeaves(&n.Nodes[i], out)
	}
	for i := range n.FloatingNodes {
		collectLeaves(&n.FloatingNodes[i], out)
	}
}

// ActiveTitle returns the title of the currently focused window.
func (m *SwayManager) ActiveTitle(ctx context.Context) (string, error) {
	raw, err := m.query(swayMsgGetTree, "")
	if err != nil {
		return "", fmt.Errorf("window/sway: get_tree: %w", err)
	}
	var root swayNode
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("window/sway: parse tree: %w", err)
	}
	title := findFocused(&root)
	if title == "" {
		return "", fmt.Errorf("window/sway: no focused window")
	}
	return title, nil
}

func findFocused(n *swayNode) string {
	if n.Focused && (n.Type == "con" || n.Type == "floating_con") {
		return n.Name
	}
	for i := range n.Nodes {
		if t := findFocused(&n.Nodes[i]); t != "" {
			return t
		}
	}
	for i := range n.FloatingNodes {
		if t := findFocused(&n.FloatingNodes[i]); t != "" {
			return t
		}
	}
	return ""
}

func (m *SwayManager) findWindow(ctx context.Context, substr string) (Info, error) {
	w, err := FindByTitle(ctx, m, substr)
	if err != nil {
		return Info{}, fmt.Errorf("window/sway: %w", err)
	}
	return w, nil
}

// swayCmd runs a sway IPC command and returns an error if sway reports failure.
func (m *SwayManager) swayCmd(cmd string) error {
	resp, err := m.query(swayMsgRunCommand, cmd)
	if err != nil {
		return err
	}
	var results []struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(resp, &results); err == nil && len(results) > 0 && !results[0].Success {
		return fmt.Errorf("window/sway: command failed: %s", results[0].Error)
	}
	return nil
}

// Activate focuses the first window whose title contains substr (case-insensitive).
func (m *SwayManager) Activate(ctx context.Context, substr string) error {
	w, err := m.findWindow(ctx, substr)
	if err != nil {
		return err
	}
	return m.swayCmd(fmt.Sprintf("[con_id=%d] focus", int64(w.ID)))
}

// Restore is a no-op on sway as it does not have a formal restore action for scratchpad/fullscreen.
func (m *SwayManager) Restore(ctx context.Context, substr string) error {
	return ErrNotSupported
}

// Move repositions the first window whose title contains substr.
// The window is made floating so it can be placed at an absolute position.
func (m *SwayManager) Move(ctx context.Context, substr string, x, y int) error {
	w, err := m.findWindow(ctx, substr)
	if err != nil {
		return err
	}
	if err := m.swayCmd(fmt.Sprintf("[con_id=%d] floating enable", int64(w.ID))); err != nil {
		return err
	}
	// Wait for sway to report the window away from its tiled origin, indicating
	// the float layout reflow is complete (up to ~500 ms).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		wins, err := m.List(ctx)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		for _, win := range wins {
			if win.ID == w.ID && (win.X != w.X || win.Y != w.Y) {
				goto ready
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
ready:
	return m.swayCmd(fmt.Sprintf("[con_id=%d] move position %d %d", int64(w.ID), x, y))
}

// Resize changes the dimensions of the first window whose title contains substr.
func (m *SwayManager) Resize(ctx context.Context, substr string, width, height int) error {
	w, err := m.findWindow(ctx, substr)
	if err != nil {
		return err
	}
	if err := m.swayCmd(fmt.Sprintf("[con_id=%d] floating enable", int64(w.ID))); err != nil {
		return err
	}
	cmd := fmt.Sprintf("[con_id=%d] resize set %d %d", int64(w.ID), width, height)
	return m.swayCmd(cmd)
}

// Close releases the persistent IPC connection.
func (m *SwayManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil {
		err := m.conn.Close()
		m.conn = nil
		return err
	}
	return nil
}

// CloseWindow kills the first window whose title contains substr.
func (m *SwayManager) CloseWindow(ctx context.Context, substr string) error {
	w, err := m.findWindow(ctx, substr)
	if err != nil {
		return err
	}
	return m.swayCmd(fmt.Sprintf("[con_id=%d] kill", int64(w.ID)))
}

// Minimize moves the first matching window to the scratchpad (sway's minimization).
func (m *SwayManager) Minimize(ctx context.Context, substr string) error {
	w, err := m.findWindow(ctx, substr)
	if err != nil {
		return err
	}
	return m.swayCmd(fmt.Sprintf("[con_id=%d] move scratchpad", int64(w.ID)))
}

// Maximize toggles fullscreen on the first matching window.
func (m *SwayManager) Maximize(ctx context.Context, substr string) error {
	w, err := m.findWindow(ctx, substr)
	if err != nil {
		return err
	}
	return m.swayCmd(fmt.Sprintf("[con_id=%d] fullscreen enable", int64(w.ID)))
}

// swayQueryOnce sends a single IPC request and returns the raw JSON response.
func swayQueryOnce(sock string, msgType uint32, payload string) ([]byte, error) {
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck

	// Write: magic(6) + length(4 LE) + type(4 LE) + payload
	pb := []byte(payload)
	msg := make([]byte, 14+len(pb))
	copy(msg[0:6], swayMagic[:])
	binary.LittleEndian.PutUint32(msg[6:10], uint32(len(pb)))
	binary.LittleEndian.PutUint32(msg[10:14], msgType)
	copy(msg[14:], pb)
	if _, err := conn.Write(msg); err != nil {
		return nil, err
	}

	// Read header: magic(6) + length(4 LE) + type(4 LE)
	hdr := make([]byte, 14)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return nil, err
	}
	if string(hdr[0:6]) != string(swayMagic[:]) {
		return nil, fmt.Errorf("bad magic")
	}
	bodyLen := binary.LittleEndian.Uint32(hdr[6:10])
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	return body, nil
}
