package workflow

import (
	"fmt"
	"strings"
)

// SplitShell parses the existing workflow token grammar.
func SplitShell(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote := byte(0)
	escaped := false
	tokenStarted := false
	emit := func() {
		if tokenStarted {
			out = append(out, cur.String())
			cur.Reset()
			tokenStarted = false
		}
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			tokenStarted = true
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			tokenStarted = true
			if inQuote == '\'' {
				cur.WriteByte(ch)
			} else {
				escaped = true
			}
		case '\'', '"':
			tokenStarted = true
			if inQuote == 0 {
				inQuote = ch
				continue
			}
			if inQuote == ch {
				inQuote = 0
				continue
			}
			cur.WriteByte(ch)
		case ' ', '\t':
			if inQuote != 0 {
				tokenStarted = true
				cur.WriteByte(ch)
				continue
			}
			emit()
		default:
			tokenStarted = true
			cur.WriteByte(ch)
		}
	}
	if escaped {
		return nil, fmt.Errorf("trailing escape")
	}
	if inQuote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	emit()
	return out, nil
}
