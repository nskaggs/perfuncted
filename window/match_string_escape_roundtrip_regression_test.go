package window

import "testing"

// Match.String produces strconv-quoted values. ParseMatchSpec must decode
// that representation without losing control characters or backslashes.
func TestMatchStringParseRoundTripQuotedControlCharacters(t *testing.T) {
	cases := []string{"line one\nline two", "tabs\tinside", "carriage\rreturn", "back\\slash", "quoted \"title\"", "control\x01byte"}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			original := Match{TitleExact: value, TitleContains: value, AppID: value, Class: value}
			parsed, err := ParseMatchSpec(original.String())
			if err != nil {
				t.Fatalf("parse %q: %v", original.String(), err)
			}
			if parsed.TitleExact != value || parsed.TitleContains != value || parsed.AppID != value || parsed.Class != value {
				t.Fatalf("round trip %q: parsed %#v", original.String(), parsed)
			}
		})
	}
}
