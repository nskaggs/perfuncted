package window

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type syntheticKWinScriptService struct {
	conn        *dbus.Conn
	mu          sync.Mutex
	svc         string
	plugin      string
	response    string
	loadCount   int
	startCount  int
	unloadCount int
	loadedPath  string
	noCallback  bool
}

var syntheticKWinServiceName = regexp.MustCompile(`org\.kde\.pflist[0-9]+_[0-9]+`)

func (s *syntheticKWinScriptService) Introspect() (string, *dbus.Error) {
	return "<node><interface name=\"" + kwinScriptIface + "\" /></node>", nil
}
func (s *syntheticKWinScriptService) LoadScript(path, plugin string) (int32, *dbus.Error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, dbus.MakeFailedError(err)
	}
	service := syntheticKWinServiceName.FindString(string(data))
	if service == "" {
		return 0, dbus.MakeFailedError(errors.New("missing callback service in script"))
	}
	var response string
	switch {
	case strings.Contains(string(data), "JSON.stringify(rows)"):
		response = `[{"id":"7","title":"Editor","app_id":"editor","class":"Editor","pid":33,"x":-100,"y":20,"width":700,"height":480,"active":true}]`
	case strings.Contains(string(data), "w ? w.caption"):
		response = "Editor"
	default:
		response = kwinScriptFoundResult
	}
	s.mu.Lock()
	s.svc = service
	s.plugin = plugin
	s.response = response
	s.loadCount++
	s.loadedPath = path
	s.mu.Unlock()
	return 12, nil
}
func (s *syntheticKWinScriptService) Start() *dbus.Error {
	s.mu.Lock()
	svc, response, skip := s.svc, s.response, s.noCallback
	s.startCount++
	s.mu.Unlock()
	if !skip {
		go func() { _ = s.conn.Object(svc, "/").Call(svc+".ReportWindows", 0, response).Err }()
	}
	return nil
}
func (s *syntheticKWinScriptService) UnloadScript(plugin string) *dbus.Error {
	s.mu.Lock()
	s.unloadCount++
	s.mu.Unlock()
	return nil
}
func newSyntheticKWinManager(t *testing.T) (*KWinScriptManager, *syntheticKWinScriptService) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon missing")
	}
	dir := t.TempDir()
	addr := "unix:path=" + filepath.Join(dir, "bus")
	daemon := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+addr)
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	var service *dbus.Conn
	var err error
	for i := 0; i < 100; i++ {
		service, err = dbusutil.SessionBusAddressContext(context.Background(), addr)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(kwinScriptSvc, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own fake KWin service: %v %v", reply, err)
	}
	provider := &syntheticKWinScriptService{conn: service}
	if err := service.ExportWithMap(provider, map[string]string{
		"LoadScript":   "loadScript",
		"Start":        "start",
		"UnloadScript": "unloadScript",
	}, kwinScriptPath, kwinScriptIface); err != nil {
		t.Fatal(err)
	}
	if err := service.Export(provider, kwinScriptPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewKWinScriptManagerForBusContext(context.Background(), addr)
	if err != nil {
		t.Fatalf("KWin script manager constructor: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, provider
}

func TestSyntheticKWinScriptListTitleAndCleanup(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wins, err := manager.List(ctx)
	if err != nil || len(wins) != 1 || wins[0].NativeID != "7" || wins[0].Title != "Editor" || wins[0].X != -100 {
		t.Fatalf("KWin window listing %v err=%v", wins, err)
	}
	name, err := manager.ActiveTitle(ctx)
	if err != nil || name != "Editor" {
		t.Fatalf("active title=%q, %v", name, err)
	}
	provider.mu.Lock()
	loads, starts, unloads, path := provider.loadCount, provider.startCount, provider.unloadCount, provider.loadedPath
	provider.mu.Unlock()
	if loads != 2 || starts != 2 || unloads != 2 {
		t.Fatalf("leaked scripts: load=%d start=%d unload=%d", loads, starts, unloads)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary compositor script not removed: %v", err)
	}
}

func TestSyntheticKWinScriptActionsUseStableID(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	actions := []struct {
		name string
		run  func() error
	}{
		{"activate", func() error { return manager.ActivateByID(ctx, "7") }},
		{"move", func() error { return manager.MoveByID(ctx, "7", -500, 25) }},
		{"resize", func() error { return manager.ResizeByID(ctx, "7", 800, 600) }},
		{"close", func() error { return manager.CloseWindowByID(ctx, "7") }},
		{"minimize", func() error { return manager.MinimizeByID(ctx, "7") }},
		{"maximize", func() error { return manager.MaximizeByID(ctx, "7") }},
		{"restore", func() error { return manager.RestoreByID(ctx, "7") }},
	}
	for _, a := range actions {
		if err := a.run(); err != nil {
			t.Fatalf("KWin %s: %v", a.name, err)
		}
	}
	provider.mu.Lock()
	count, unloaded := provider.loadCount, provider.unloadCount
	provider.mu.Unlock()
	if count != len(actions) || unloaded != len(actions) {
		t.Fatalf("script lifecycle %d loaded, %d unloaded", count, unloaded)
	}
}

func TestSyntheticKWinScriptCancelledCallbackCleansLoadedScripts(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	provider.mu.Lock()
	provider.noCallback = true
	provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, err := manager.ActiveTitle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing callback should time out: %v", err)
	}
	provider.mu.Lock()
	loaded, unloaded := provider.loadCount, provider.unloadCount
	provider.mu.Unlock()
	if loaded != 1 || unloaded != 1 {
		t.Fatalf("canceled script leaked: load=%d unload=%d", loaded, unloaded)
	}
}
