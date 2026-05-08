package window

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func int32Ptr(v int32) *int32    { return &v }
func uint64Ptr(v uint64) *uint64 { return &v }

func TestParseMatchSpec(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want Match
	}{
		{
			name: "title substring shorthand",
			spec: "editor",
			want: Match{TitleContains: "editor"},
		},
		{
			name: "structured match",
			spec: `title="Exact Title" app_id=org.example class=Example pid=42 id=7 state:active state:-minimized state:fullscreen visible:true`,
			want: Match{
				TitleExact:  "Exact Title",
				AppID:       "org.example",
				Class:       "Example",
				PID:         int32Ptr(42),
				ID:          uint64Ptr(7),
				Active:      boolPtr(true),
				Minimized:   boolPtr(false),
				Fullscreen:  boolPtr(true),
				VisibleOnly: true,
			},
		},
		{
			name: "substring key",
			spec: `title~=browser`,
			want: Match{TitleContains: "browser"},
		},
		{
			name: "visible false",
			spec: `visible:false`,
			want: Match{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMatchSpec(tt.spec)
			if err != nil {
				t.Fatalf("ParseMatchSpec(%q) error = %v", tt.spec, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseMatchSpec(%q) = %+v, want %+v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestParseMatchSpec_Invalid(t *testing.T) {
	tests := []string{
		`pid=abc`,
		`id=abc`,
		`state:bogus`,
		`title="unterminated`,
	}
	for _, spec := range tests {
		t.Run(spec, func(t *testing.T) {
			if _, err := ParseMatchSpec(spec); err == nil {
				t.Fatalf("ParseMatchSpec(%q) expected error, got nil", spec)
			}
		})
	}
}

func TestMatch_Matches(t *testing.T) {
	info := Info{
		ID:         7,
		Title:      "Exact Title",
		AppID:      "org.example",
		Class:      "Example",
		PID:        42,
		Active:     true,
		Minimized:  false,
		Maximized:  true,
		Fullscreen: true,
	}

	tests := []struct {
		name  string
		match Match
		want  bool
	}{
		{name: "substring", match: Match{TitleContains: "exact"}, want: true},
		{name: "exact title", match: Match{TitleExact: "exact title"}, want: true},
		{name: "app id", match: Match{AppID: "ORG.EXAMPLE"}, want: true},
		{name: "class", match: Match{Class: "example"}, want: true},
		{name: "pid", match: Match{PID: int32Ptr(42)}, want: true},
		{name: "id", match: Match{ID: uint64Ptr(7)}, want: true},
		{name: "active", match: Match{Active: boolPtr(true)}, want: true},
		{name: "minimized false", match: Match{Minimized: boolPtr(false)}, want: true},
		{name: "maximized", match: Match{Maximized: boolPtr(true)}, want: true},
		{name: "fullscreen", match: Match{Fullscreen: boolPtr(true)}, want: true},
		{name: "visible only", match: Match{VisibleOnly: true}, want: true},
		{name: "state negation", match: Match{Minimized: boolPtr(false)}, want: true},
		{name: "wrong pid", match: Match{PID: int32Ptr(99)}, want: false},
		{name: "wrong class", match: Match{Class: "terminal"}, want: false},
	}

	minimized := Info{ID: 9, Title: "Hidden", Minimized: true}
	if (Match{VisibleOnly: true}).Matches(minimized) {
		t.Fatal("VisibleOnly should reject minimized windows")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.match.Matches(info); got != tt.want {
				t.Fatalf("Match(%+v).Matches() = %v, want %v", tt.match, got, tt.want)
			}
		})
	}
}

func TestFindByMatch(t *testing.T) {
	m := &fakeManager{wins: []Info{
		{ID: 1, Title: "Launcher", AppID: "org.example.launcher", Class: "Launcher"},
		{ID: 2, Title: "Editor", AppID: "org.example.editor", Class: "Code", PID: 42, Active: true},
	}}

	w, err := Find(context.Background(), m, Match{AppID: "org.example.editor"})
	if err != nil {
		t.Fatalf("Find(app id) unexpected error: %v", err)
	}
	if w.ID != 2 {
		t.Fatalf("Find(app id) returned ID %d, want 2", w.ID)
	}

	w, err = Find(context.Background(), m, Match{TitleExact: "editor", Active: boolPtr(true)})
	if err != nil {
		t.Fatalf("Find(exact title) unexpected error: %v", err)
	}
	if w.ID != 2 {
		t.Fatalf("Find(exact title) returned ID %d, want 2", w.ID)
	}
}

func TestFindByMatch_NotFound(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "Foo"}}}
	_, err := Find(context.Background(), m, Match{Class: "missing"})
	if err == nil {
		t.Fatal("Find() expected error, got nil")
	}
	if !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("Find() error = %v, want ErrWindowNotFound", err)
	}
	if got := err.Error(); got == "" || got == "<nil>" {
		t.Fatalf("unexpected empty error string: %q", got)
	}
}

func TestApplyToplevelString(t *testing.T) {
	encode := func(s string) []byte {
		b := make([]byte, 4+len(s))
		b[0] = byte(len(s))
		copy(b[4:], s)
		return b
	}

	info := &Info{}
	if !applyToplevelString(info, 0, encode("Title")) {
		t.Fatal("applyToplevelString(title) returned false")
	}
	if info.Title != "Title" {
		t.Fatalf("title = %q, want %q", info.Title, "Title")
	}
	if !applyToplevelString(info, 1, encode("org.example")) {
		t.Fatal("applyToplevelString(app_id) returned false")
	}
	if info.AppID != "org.example" {
		t.Fatalf("app id = %q, want %q", info.AppID, "org.example")
	}
	if applyToplevelString(info, 99, encode("ignored")) {
		t.Fatal("applyToplevelString for unknown opcode returned true")
	}
}

func ExampleMatch_String() {
	m := Match{TitleContains: "editor", AppID: "org.example", Active: boolPtr(true)}
	fmt.Println(m.String())
	// Output:
	// title~="editor" app_id="org.example" active=true
}
