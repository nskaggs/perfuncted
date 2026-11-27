//go:build integration && linux

package live_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in real-desktop test sends input only to a disposable Firefox
// window. It verifies application delivery, not merely successful ioctls.
// Run from the owner-authorized unlocked KDE session with PF_BIN pointing to
// the candidate executable. Do not run while someone is using the desktop.
func TestLiveFirefoxKeyboardRegression(t *testing.T) {
	if os.Getenv("PF_LIVE_KEYBOARD_TEST") != "1" {
		t.Skip("requires explicitly authorized live desktop")
	}
	bin := os.Getenv("PF_BIN")
	if !filepath.IsAbs(bin) {
		t.Fatal("PF_BIN must be absolute")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run := func(args ...string) string {
		t.Helper()
		out, e := exec.CommandContext(ctx, bin, args...).CombinedOutput()
		if e != nil {
			t.Fatalf("pf %v: %v: %s", args, e, out)
		}
		return strings.TrimSpace(string(out))
	}
	type win struct{ NativeID, Title string }
	active := func() win {
		t.Helper()
		var w win
		if e := json.Unmarshal([]byte(run("window", "active-info")), &w); e != nil {
			t.Fatal(e)
		}
		return w
	}
	prior := active().NativeID
	dir := t.TempDir()
	token := fmt.Sprintf("PF-KEY-REG-%d", time.Now().UnixNano())
	file := filepath.Join(dir, "fixture.html")
	// Title reflects exact DOM text or URL fragment, providing independent
	// observable postconditions for content and browser-chrome typing.
	html := fmt.Sprintf(`<!doctype html><title>%s</title><input autofocus style="width:90%%"><script>const prefix=%q;const field=document.querySelector('input');field.addEventListener('input',()=>document.title=prefix+'-text-'+field.value);function hash(){if(location.hash)document.title=prefix+'-hash-'+location.hash.slice(1)};addEventListener('hashchange',hash);hash()</script>`, token, token)
	if e := os.WriteFile(file, []byte(html), 0600); e != nil {
		t.Fatal(e)
	}
	location := (&url.URL{Scheme: "file", Path: file}).String()
	if out, e := exec.CommandContext(ctx, "firefox", "--new-window", location).CombinedOutput(); e != nil {
		t.Fatalf("Firefox fixture: %v %s", e, out)
	}
	run("window", "wait-for", token, "--timeout", "8s")
	w := active()
	if w.NativeID == "" || !strings.HasPrefix(w.Title, token) || w.NativeID == prior {
		t.Fatal("fixture identity not established")
	}
	id := w.NativeID
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(c, bin, "window", "close-id", id).Run()
	}()
	guard := func() {
		t.Helper()
		if active().NativeID != id {
			t.Fatal("fixture lost focus; no input sent")
		}
	}
	dispatch := func(args ...string) { t.Helper(); guard(); run(append([]string{"input"}, args...)...) }
	expect := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			v := active()
			if v.NativeID != id {
				t.Fatal("fixture lost focus")
			}
			if v.Title == want || strings.HasPrefix(v.Title, want+" — ") {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("application delivery mismatch: want %q got %q", want, v.Title)
			}
			time.Sleep(40 * time.Millisecond)
		}
	}
	expected := ""
	for i := 1; i <= 20; i++ {
		s := fmt.Sprintf("aB%dz", i)
		dispatch("type-literal", s)
		expected += s
		expect(token + "-text-" + expected)
	}
	long := strings.Repeat("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", 3)
	dispatch("type", "{ctrl+a}")
	dispatch("type-literal", long)
	expect(token + "-text-" + long)
	// The address already contains our local file URL. Preserve its prefix and
	// append only local fragments so no external navigation is intended.
	hash := ""
	for _, part := range []string{"abcdefghijklmnopqrstuvwxyz0123456789", "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "runtime-this-firefox-0123456789"} {
		dispatch("type", "{ctrl+l}")
		dispatch("type", "{end}")
		suffix := part
		if hash == "" {
			suffix = "#" + part
		}
		dispatch("type-literal", suffix)
		dispatch("type", "{enter}")
		hash += part
		expect(token + "-hash-" + hash)
	}
}
