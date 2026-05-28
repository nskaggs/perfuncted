package window

import (
	"context"
	"errors"
	"testing"
)

func TestParseKWinWindowListPreservesWindowIDs(t *testing.T) {
	data := "42\tWindow One\torg.app.one\tAppOne\t101\t10\t20\t300.5\t400\n0x2b\tWindow Two\torg.app.two\tAppTwo\t202\t30.9\t40\t500\t600.1\n"

	got := parseKWinWindowList(data)
	if len(got) != 2 {
		t.Fatalf("parseKWinWindowList len = %d, want 2", len(got))
	}
	if got[0].ID != 42 {
		t.Fatalf("first ID = %d, want 42", got[0].ID)
	}
	if got[0].Title != "Window One" || got[0].AppID != "org.app.one" || got[0].Class != "AppOne" || got[0].PID != 101 {
		t.Fatalf("first window = %+v", got[0])
	}
	if got[0].W != 300 || got[0].H != 400 {
		t.Fatalf("first geometry = %dx%d, want 300x400", got[0].W, got[0].H)
	}
	if got[1].ID != 0x2b {
		t.Fatalf("second ID = %d, want %d", got[1].ID, 0x2b)
	}
	if got[1].X != 30 || got[1].H != 600 {
		t.Fatalf("second geometry = %+v", got[1])
	}
}

func TestKWinRunScriptCanceledContextShortCircuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	_, err := (&KWinScriptManager{}).runScript(ctx, func(string) string {
		called = true
		return ""
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runScript error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("runScript called buildJS after context cancellation")
	}
}

func TestKWinRunScriptRequiresInitializedBackend(t *testing.T) {
	_, err := (&KWinScriptManager{}).runScript(context.Background(), func(string) string { return "" })
	if err == nil || err.Error() != "window/kwinscript: backend not initialised" {
		t.Fatalf("runScript error = %v, want backend not initialised", err)
	}
}
