// Package keymap provides key-name resolution and the Send syntax parser.
//
// The Send syntax uses braced key names and modifier prefixes:
//
//	"Hello, world{!}{Left}"
//
// Literal text is sent character-by-character. Braced expressions send special
// keys or key combinations:
//
//	{enter}          — press and release Enter
//	{shift down}     — hold Shift
//	{shift up}       — release Shift
//	{ctrl+shift+s}   — press Ctrl+Shift+S
//	{^!+a}           — shorthand for Ctrl+Alt+Shift+A
//
// Modifier shorthand (only valid inside braces):
//	^ = ctrl,  ! = alt,  + = shift,  # = super
package keymap

import (
	"fmt"
	"strings"
	"unicode"
)

// Token is a parsed element of a Send string.
type Token struct {
	// Text is literal text to type (for Text tokens).
	Text string
	// Keys is the list of key names to press simultaneously (for Combo tokens).
	Keys []string
	// Down is true for press, false for release. Only used for single-key tokens.
	Down *bool
}

// IsCombo returns true if the token represents a key combination.
func (t Token) IsCombo() bool {
	return len(t.Keys) > 0
}

// ParseSend parses a send-string into a sequence of tokens.
func ParseSend(input string) ([]Token, error) {
	var tokens []Token
	var literal strings.Builder
	i := 0
	for i < len(input) {
		if input[i] == '{' {
			// Flush pending literal text.
			if literal.Len() > 0 {
				tokens = append(tokens, Token{Text: literal.String()})
				literal.Reset()
			}
			// Find closing brace.
			j := strings.IndexByte(input[i:], '}')
			if j == -1 {
				return nil, fmt.Errorf("unclosed brace at offset %d", i)
			}
			j += i
			expr := input[i+1 : j]
			tok, err := parseBraced(expr)
			if err != nil {
				return nil, fmt.Errorf("at offset %d: %w", i, err)
			}
			tokens = append(tokens, tok)
			i = j + 1
		} else {
			literal.WriteByte(input[i])
			i++
		}
	}
	if literal.Len() > 0 {
		tokens = append(tokens, Token{Text: literal.String()})
	}
	return tokens, nil
}

// parseBraced parses the expression inside a single pair of braces.
func parseBraced(expr string) (Token, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return Token{}, fmt.Errorf("empty braces {}")
	}

	// Check for {key down} / {key up} syntax.
	lower := strings.ToLower(expr)
	for _, suffix := range []string{" down", " up"} {
		if strings.HasSuffix(lower, suffix) {
			keyPart := strings.TrimSpace(expr[:len(expr)-len(suffix)])
			keyName, err := resolveSendKey(keyPart)
			if err != nil {
				return Token{}, err
			}
			down := suffix == " down"
			return Token{Keys: []string{keyName}, Down: &down}, nil
		}
	}

	// Check for modifier shorthand: ^!+# prefixes.
	if modKeys, rest, ok := parseModPrefixes(expr); ok {
		if rest == "" {
			// Just modifiers — treat as a combo of the modifiers themselves.
			return Token{Keys: modKeys}, nil
		}
		keyName, err := resolveSendKey(rest)
		if err != nil {
			return Token{}, err
		}
		return Token{Keys: append(modKeys, keyName)}, nil
	}

	// Check for combo with + separator: "ctrl+shift+s"
	if strings.Contains(expr, "+") {
		parts := strings.Split(expr, "+")
		keys := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				return Token{}, fmt.Errorf("empty key in combo %q", expr)
			}
			keyName, err := resolveSendKey(p)
			if err != nil {
				return Token{}, err
			}
			keys = append(keys, keyName)
		}
		return Token{Keys: keys}, nil
	}

	// Single key name.
	keyName, err := resolveSendKey(expr)
	if err != nil {
		return Token{}, err
	}
	return Token{Keys: []string{keyName}}, nil
}

// parseModPrefixes extracts leading ^!+# modifier prefixes.
// Returns the modifier key names, the remaining string, and true if any prefix was found.
func parseModPrefixes(s string) ([]string, string, bool) {
	var mods []string
	i := 0
	for i < len(s) {
		switch s[i] {
		case '^':
			mods = append(mods, "ctrl")
			i++
		case '!':
			mods = append(mods, "alt")
			i++
		case '+':
			mods = append(mods, "shift")
			i++
		case '#':
			mods = append(mods, "super")
			i++
		default:
			goto done
		}
	}
done:
	if len(mods) == 0 {
		return nil, s, false
	}
	// Skip a literal '+' separator between prefix and key if present.
	rest := s[i:]
	rest = strings.TrimLeft(rest, "+")
	return mods, rest, true
}

// resolveSendKey maps a key name from send syntax to a canonical key name.
// Accepts both canonical names ("enter", "ctrl") and single characters ("a", " ").
func resolveSendKey(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("empty key name")
	}

	// Single character: pass through as-is (will be typed).
	if len([]rune(name)) == 1 {
		return name, nil
	}

	// Look up canonical name.
	lower := strings.ToLower(name)
	if _, ok := FromString(lower); ok {
		return lower, nil
	}

	return "", fmt.Errorf("unknown key %q", name)
}

// EscapeSend escapes a literal string so it is treated as plain text
// by ParseSend (wraps special characters in braces).
func EscapeSend(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '{', '}', '^', '!', '+', '#':
			b.WriteString("{")
			b.WriteRune(r)
			b.WriteString("}")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IsSendSyntax reports whether s contains any send-syntax special sequences.
func IsSendSyntax(s string) bool {
	return strings.ContainsAny(s, "{}^+!#")
}

// SplitSendForDisplay splits a send string into human-readable parts for logging.
func SplitSendForDisplay(input string) []string {
	tokens, err := ParseSend(input)
	if err != nil {
		return []string{input}
	}
	var parts []string
	for _, t := range tokens {
		if t.Text != "" {
			parts = append(parts, t.Text)
		} else if t.IsCombo() {
			if t.Down != nil {
				if *t.Down {
					parts = append(parts, "↓"+strings.Join(t.Keys, "+"))
				} else {
					parts = append(parts, "↑"+strings.Join(t.Keys, "+"))
				}
			} else {
				parts = append(parts, strings.Join(t.Keys, "+"))
			}
		}
	}
	return parts
}

// CanonicalKeyName returns the canonical name for a key suitable for KeyDown/KeyUp.
// Returns the name and true if it's a known key, or the original string and false.
func CanonicalKeyName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	lower := strings.ToLower(name)
	if _, ok := FromString(lower); ok {
		return lower, true
	}
	return name, false
}

// IsSingleRuneKey reports whether name is a single character (not a named key).
func IsSingleRuneKey(name string) bool {
	return len([]rune(name)) == 1
}

// ModifierKeys returns the canonical modifier key names for ^!+# shorthand.
func ModifierKeys(prefix string) []string {
	var mods []string
	for _, r := range prefix {
		switch unicode.ToLower(r) {
		case '^':
			mods = append(mods, "ctrl")
		case '!':
			mods = append(mods, "alt")
		case '+':
			mods = append(mods, "shift")
		case '#':
			mods = append(mods, "super")
		}
	}
	return mods
}
