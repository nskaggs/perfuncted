package window

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestParseKWinWindowListPreservesWindowIDs(t *testing.T) {
	data := `[ {"id":"42","title":"Window One\twith tab","app_id":"org.app.one","class":"AppOne","pid":101,"x":10,"y":20,"width":300.5,"height":400},
{"id":"0x2b","title":"Window Two","app_id":"org.app.two","class":"AppTwo","pid":202,"x":30.9,"y":40,"width":500,"height":600.1},
{"id":"{bfe19e8e-18f4-4d48}","title":"Window Three","app_id":"org.app.three","class":"AppThree","pid":303,"x":1,"y":2,"width":3,"height":4} ]`

	got, err := parseKWinWindowList(data)
	if err != nil {
		t.Fatalf("parseKWinWindowList: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("parseKWinWindowList len = %d, want 3", len(got))
	}
	if got[0].ID != 42 {
		t.Fatalf("first ID = %d, want 42", got[0].ID)
	}
	if got[0].Title != "Window One\twith tab" || got[0].AppID != "org.app.one" || got[0].Class != "AppOne" || got[0].PID != 101 {
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
	if got[2].NativeID != "{bfe19e8e-18f4-4d48}" ||
		got[2].StableID() != "{bfe19e8e-18f4-4d48}" {
		t.Fatalf("third stable ID = %q", got[2].StableID())
	}
}

func TestParseKWinWindowListRejectsMalformedJSON(t *testing.T) {
	if _, err := parseKWinWindowList("not-json"); err == nil {
		t.Fatal("parseKWinWindowList accepted malformed JSON")
	}
}

func TestParseKWinWindowListRejectsInvalidGeometry(t *testing.T) {
	for _, data := range []string{
		`[{"id":"1","width":1e20,"height":10}]`,
		`[{"id":"1","width":-1,"height":10}]`,
	} {
		if _, err := parseKWinWindowList(data); err == nil {
			t.Fatalf("parseKWinWindowList accepted invalid geometry %s", data)
		}
	}
}

func TestParseKWinWindowListUsesJSONEscaping(t *testing.T) {
	data, err := json.Marshal([]kwinWindowRow{{ID: "17", Title: "line 1\nline 2", AppID: "org.example"}})
	if err != nil {
		t.Fatalf("marshal test row: %v", err)
	}
	got, err := parseKWinWindowList(string(data))
	if err != nil {
		t.Fatalf("parseKWinWindowList: %v", err)
	}
	if len(got) != 1 || got[0].Title != "line 1\nline 2" {
		t.Fatalf("parsed windows = %#v, want preserved title", got)
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
