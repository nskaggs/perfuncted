//go:build linux

package gnomebridge

import (
	"slices"
	"strings"
	"testing"
)

// GNOME extension identifiers are supplied by gsettings as quoted GVariant
// strings. Escapes must round-trip without turning one identifier into two.
func TestGSettingsExtensionQuoteAndParseRoundTrip(t *testing.T) {
	values := []string{
		"ordinary@example.org",
		"name'with-quote@example.org",
		`backslash\component@example.org`,
		`combined\'characters@example.org`,
		`double"quote@example.org`,
	}
	encoded := "[" + strings.Join(mapStrings(values, quoteGVariantString), ", ") + "]"
	if got := parseEnabledExtensions(encoded); !slices.Equal(got, values) {
		t.Fatalf("quoted GVariant round-trip: encoded=%q got=%q want=%q", encoded, got, values)
	}
}

func TestGSettingsExtensionParserAcceptsEscapedQuotedValues(t *testing.T) {
	input := `["gnome\\extension@example.org", 'quoted\'identifier@example.org', 'plain@example.org']`
	want := []string{`gnome\extension@example.org`, "quoted'identifier@example.org", "plain@example.org"}
	if got := parseEnabledExtensions(input); !slices.Equal(got, want) {
		t.Fatalf("parsed escaped GVariant values: got=%q want=%q", got, want)
	}
}
