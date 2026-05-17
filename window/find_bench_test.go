package window

import (
	"context"
	"testing"
)

func BenchmarkMatchMatches_TitleContains(b *testing.B) {
	info := Info{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox", Active: true}
	m := Match{TitleContains: "firefox"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Matches(info)
	}
}

func BenchmarkMatcherMatches_TitleContains(b *testing.B) {
	info := Info{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox", Active: true}
	matcher := CompileMatch(Match{TitleContains: "firefox"})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = matcher.Matches(info)
	}
}

func BenchmarkMatchMatches_TitleExact(b *testing.B) {
	info := Info{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox"}
	m := Match{TitleExact: "firefox web browser"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Matches(info)
	}
}

func BenchmarkMatchMatches_MultiField(b *testing.B) {
	active := true
	info := Info{ID: 7, Title: "Editor", AppID: "org.example.editor", PID: 42, Active: true}
	m := Match{TitleContains: "editor", AppID: "org.example.editor", Active: &active}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Matches(info)
	}
}

func BenchmarkFindByTitle(b *testing.B) {
	wins := make([]Info, 20)
	for i := range wins {
		wins[i] = Info{ID: uint64(i + 1), Title: "Window " + string(rune('A'+i)), AppID: "org.example.app"}
	}
	wins[19].Title = "Target Window"
	m := &fakeManager{wins: wins}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = FindByTitle(context.Background(), m, "target")
	}
}

func BenchmarkFind_MatchByAppID(b *testing.B) {
	wins := make([]Info, 20)
	for i := range wins {
		wins[i] = Info{ID: uint64(i + 1), Title: "Window", AppID: "org.other.app"}
	}
	wins[19].AppID = "org.target.app"
	m := &fakeManager{wins: wins}
	match := Match{AppID: "org.target.app"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Find(context.Background(), m, match)
	}
}
