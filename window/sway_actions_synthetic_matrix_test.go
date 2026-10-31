package window

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
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
			dir := t.TempDir()
			sock := filepath.Join(dir, "sway-ipc.123.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				kind, _, err := readSwayMessage(conn)
				if err != nil {
					serverDone <- err
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
