package window

import (
	"strings"
	"testing"
)

// TestContainsFoldLowerMatchesLowerLowerContains proves that the
// allocation-free fold matches the previous strings.Contains(strings.ToLower,
// strings.ToLower) semantics exactly, including runes whose lowercase form has
// a different byte length.
func TestContainsFoldLowerMatchesLowerLowerContains(t *testing.T) {
	corpus := []struct{ s, sub string }{
		{"Firefox Web Browser", "firefox"},
		{"Firefox Web Browser", "WEB"},
		{"Firefox Web Browser", "browser"},
		{"Firefox Web Browser", ""},
		{"", ""},
		{"", "x"},
		{"a", "abc"},
		{"abc", "abcd"},
		{"CAFÉ", "café"},
		{"café", "CAFÉ"},
		{"café au lait", "é au"},
		{"STRASSE", "straße"},
		{"straße", "STRASSE"},
		{"İstanbul", "istanbul"},
		{"Istanbul", "İstanbul"},
		{"ı", "I"},
		{"I", "ı"},
		{"ẞETA", "ßeta"},
		{"eßential", "ESSENTIAL"},
		{"aİb", "ai"},
		{"aİb", "aİ"},
		{"A" + "İ" + "B", "aib"},
		{"no match here", "zzz"},
		{"abc", "b"},
		{"abc", "bc"},
		{"abc", "ab"},
		{"AbC", "abc"},
		{"x1y2", "1Y"},
		{"mixed CASE string", "CASE"},
		{"ÉÉ", "éé"},
		{"ÉÉÉ", "é"},
		{"aeiou", "iou"},
		{"привет", "ПРИВЕТ"},
		{"ПРИВЕТ", "рив"},
		{"déjà vu", "DÉJÀ"},
		{"Resumé", "resume"},
		{"Resumé", "é"},
		{"Ωmega", "omega"},
		{"omega", "Ω"},
	}
	for _, pair := range corpus {
		got := containsFoldLower(pair.s, strings.ToLower(pair.sub))
		want := strings.Contains(strings.ToLower(pair.s), strings.ToLower(pair.sub))
		if got != want {
			t.Errorf("containsFoldLower(%q, %q) = %v, want %v", pair.s, strings.ToLower(pair.sub), got, want)
		}
	}
}

func TestMatcherTitleContainsFoldEquivalent(t *testing.T) {
	infos := []Info{
		{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox", Active: true},
		{ID: 2, Title: "CAFÉ", AppID: "org.example"},
		{ID: 3, Title: "İstanbul Airport", AppID: "org.example"},
		{ID: 4, Title: "STRASSE", AppID: "org.example"},
		{ID: 5, Title: "Window 5", AppID: "org.example", Minimized: true},
	}
	substrings := []string{"firefox", "café", "İstanbul", "strasse", "straße", "window", ""}
	for _, title := range infos {
		for _, sub := range substrings {
			previous := strings.Contains(strings.ToLower(title.Title), strings.ToLower(sub))
			match := Match{TitleContains: sub}
			if got := match.Matches(title); got != previous {
				t.Errorf("Match{TitleContains:%q}.Matches(%q) = %v, want %v", sub, title.Title, got, previous)
			}
			matcher := CompileMatch(Match{TitleContains: sub})
			if got := matcher.Matches(title); got != previous {
				t.Errorf("CompileMatch(Match{TitleContains:%q}).Matches(%q) = %v, want %v", sub, title.Title, got, previous)
			}
		}
	}
}
