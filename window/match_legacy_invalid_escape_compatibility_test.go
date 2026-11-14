package window

import (
	"reflect"
	"testing"
)

// Older match specifications treated unknown backslash sequences as an
// escaped literal character. These are not valid Go quoted string escapes,
// so tokenizer fallback must retain the legacy meaning without leaking a
// backslash or dropping the rest of the predicate.
func TestTokenizeMatchSpecLegacyUnknownQuotedEscapesRemainCompatible(t *testing.T) {
	for _, tc := range []struct {
		name, spec string
		want       []string
	}{
		{"single unknown escape", `title="folder\q"`, []string{"title=folderq"}},
		{"mixed escaped slash and invalid escape", `class='a\q\\b'`, []string{`class=aq\b`}},
		{"quoted path with unknown escape and separators", `title="first\q second" active`, []string{"title=firstq second", "active"}},
		{"repeated legacy escapes", `app="a\zb\yc"`, []string{"app=azbyc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tokenizeMatchSpec(tc.spec)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("legacy match tokenization %q: got=%q want=%q err=%v", tc.spec, got, tc.want, err)
			}
		})
	}
}

func TestMatchSpecificationLegacyEscapeKeepsPredicatesAndRejectsUnterminatedQuotes(t *testing.T) {
	m, err := ParseMatchSpec(`title="Firefox\q Browser" active`)
	if err != nil {
		t.Fatal(err)
	}
	if m.TitleExact != "Firefoxq Browser" || m.Active == nil || !*m.Active {
		t.Fatalf("legacy escape corrupted parsed predicates: %+v", m)
	}
	if _, err := ParseMatchSpec(`title="Firefox\q`); err == nil {
		t.Fatal("unterminated legacy quote accepted")
	}
}
