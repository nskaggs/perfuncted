// Package cliname owns the method-to-command naming rule shared by the CLI
// generator and the generated/API drift verifier.
package cliname

import (
	"strings"
	"unicode"
)

// Hyphenate converts a Go method name to its command spelling.
func Hyphenate(s string) string {
	if s == "" {
		return ""
	}
	var words []string
	var buf []rune
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		ch := r[i]
		if i > 0 && unicode.IsUpper(ch) && (unicode.IsLower(r[i-1]) || (i+1 < len(r) && unicode.IsLower(r[i+1]))) {
			if len(buf) > 0 {
				words = append(words, string(buf))
				buf = []rune{}
			}
		}
		buf = append(buf, unicode.ToLower(ch))
	}
	if len(buf) > 0 {
		words = append(words, string(buf))
	}
	return strings.Join(words, "-")
}
