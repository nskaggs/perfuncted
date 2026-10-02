package accessibility

import "testing"

func benchmarkStrings() []string {
	return []string{
		"org.gnome.Nautilus", // bus name, repeated per node
		"/org/gnome/Nautilus/desktop/window/1234567890/content", // object path
		"Push button", // role
		"Open Folder", // short name
		"Some longer label text that a real accessibility tree carries on a leaf node",
		"Editable text content for a text field that the user can type into and that carries a fair amount of content",
		"https://example.invalid/some/reasonably/long/url/path?query=1&other=2",
		"Ünïcödé ñame with multibyte runes and more text after them to scan",
		"",
	}
}

func BenchmarkJSONStringSize(b *testing.B) {
	values := benchmarkStrings()
	b.ReportAllocs()
	for b.Loop() {
		total := 0
		for _, v := range values {
			total += jsonStringSize(v)
		}
		if total == 0 {
			b.Fatal("zero total")
		}
	}
}

func BenchmarkJSONStringSizeASCIIOnly(b *testing.B) {
	v := "Some longer label text that a real accessibility tree carries on a leaf node"
	b.ReportAllocs()
	for b.Loop() {
		if jsonStringSize(v) == 0 {
			b.Fatal("zero size")
		}
	}
}

func BenchmarkJSONStringSizeRepeatedBusName(b *testing.B) {
	v := "org.gnome.Nautilus"
	b.ReportAllocs()
	for b.Loop() {
		for range 64 {
			_ = jsonStringSize(v)
		}
	}
}
