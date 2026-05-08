package window

import (
	"fmt"
	"strconv"
	"strings"
)

// Match describes a window selection predicate.
//
// Zero-value fields mean "do not care". Text matches are case-insensitive.
type Match struct {
	TitleContains string
	TitleExact    string
	AppID         string
	Class         string
	PID           *int32
	ID            *uint64
	Active        *bool
	Minimized     *bool
	Maximized     *bool
	Fullscreen    *bool
	VisibleOnly   bool
}

// Matches reports whether info satisfies m.
func (m Match) Matches(info Info) bool {
	if m.TitleContains != "" && !strings.Contains(strings.ToLower(info.Title), strings.ToLower(m.TitleContains)) {
		return false
	}
	if m.TitleExact != "" && !strings.EqualFold(info.Title, m.TitleExact) {
		return false
	}
	if m.AppID != "" && !strings.EqualFold(info.AppID, m.AppID) {
		return false
	}
	if m.Class != "" && !strings.EqualFold(info.Class, m.Class) {
		return false
	}
	if m.PID != nil && info.PID != *m.PID {
		return false
	}
	if m.ID != nil && info.ID != *m.ID {
		return false
	}
	if m.Active != nil && info.Active != *m.Active {
		return false
	}
	if m.Minimized != nil && info.Minimized != *m.Minimized {
		return false
	}
	if m.Maximized != nil && info.Maximized != *m.Maximized {
		return false
	}
	if m.Fullscreen != nil && info.Fullscreen != *m.Fullscreen {
		return false
	}
	if m.VisibleOnly && info.Minimized {
		return false
	}
	return true
}

// String returns a compact human-readable representation of m.
func (m Match) String() string {
	var parts []string
	if m.TitleExact != "" {
		parts = append(parts, "title="+strconv.Quote(m.TitleExact))
	}
	if m.TitleContains != "" {
		parts = append(parts, "title~="+strconv.Quote(m.TitleContains))
	}
	if m.AppID != "" {
		parts = append(parts, "app_id="+strconv.Quote(m.AppID))
	}
	if m.Class != "" {
		parts = append(parts, "class="+strconv.Quote(m.Class))
	}
	if m.PID != nil {
		parts = append(parts, fmt.Sprintf("pid=%d", *m.PID))
	}
	if m.ID != nil {
		parts = append(parts, fmt.Sprintf("id=%d", *m.ID))
	}
	if m.Active != nil {
		parts = append(parts, fmt.Sprintf("active=%t", *m.Active))
	}
	if m.Minimized != nil {
		parts = append(parts, fmt.Sprintf("minimized=%t", *m.Minimized))
	}
	if m.Maximized != nil {
		parts = append(parts, fmt.Sprintf("maximized=%t", *m.Maximized))
	}
	if m.Fullscreen != nil {
		parts = append(parts, fmt.Sprintf("fullscreen=%t", *m.Fullscreen))
	}
	if m.VisibleOnly {
		parts = append(parts, "visible-only")
	}
	if len(parts) == 0 {
		return "<any window>"
	}
	return strings.Join(parts, " ")
}

// ParseMatchSpec parses a small match specification language.
//
// Supported forms:
//   - bare token: title substring shorthand
//   - title=<exact>
//   - title~=<substring>
//   - app_id=..., class=..., pid=..., id=...
//   - active, minimized, maximized, fullscreen, visible-only
func ParseMatchSpec(spec string) (Match, error) {
	tokens, err := tokenizeMatchSpec(spec)
	if err != nil {
		return Match{}, err
	}
	var m Match
	for _, tok := range tokens {
		key, val, hasValue := strings.Cut(tok, "=")
		if !hasValue {
			key, val, hasValue = strings.Cut(tok, ":")
		}
		if !hasValue {
			switch strings.ToLower(tok) {
			case "active":
				t := true
				m.Active = &t
			case "minimized":
				t := true
				m.Minimized = &t
			case "maximized":
				t := true
				m.Maximized = &t
			case "fullscreen":
				t := true
				m.Fullscreen = &t
			case "visible", "visible-only":
				m.VisibleOnly = true
			default:
				if m.TitleContains != "" {
					return Match{}, fmt.Errorf("window: multiple title substring terms in %q", spec)
				}
				m.TitleContains = tok
			}
			continue
		}

		switch strings.ToLower(strings.TrimSuffix(key, "~")) {
		case "title":
			if strings.HasSuffix(key, "~") {
				m.TitleContains = val
			} else {
				m.TitleExact = val
			}
		case "title~":
			m.TitleContains = val
		case "app", "app_id", "appid":
			m.AppID = val
		case "class":
			m.Class = val
		case "pid":
			v, err := strconv.ParseInt(val, 10, 32)
			if err != nil {
				return Match{}, fmt.Errorf("window: parse pid %q: %w", val, err)
			}
			pid := int32(v)
			m.PID = &pid
		case "id":
			v, err := strconv.ParseUint(val, 10, 64)
			if err != nil {
				return Match{}, fmt.Errorf("window: parse id %q: %w", val, err)
			}
			id := uint64(v)
			m.ID = &id
		case "active":
			b, err := parseMatchBool(val)
			if err != nil {
				return Match{}, fmt.Errorf("window: parse active %q: %w", val, err)
			}
			m.Active = &b
		case "minimized":
			b, err := parseMatchBool(val)
			if err != nil {
				return Match{}, fmt.Errorf("window: parse minimized %q: %w", val, err)
			}
			m.Minimized = &b
		case "maximized":
			b, err := parseMatchBool(val)
			if err != nil {
				return Match{}, fmt.Errorf("window: parse maximized %q: %w", val, err)
			}
			m.Maximized = &b
		case "fullscreen":
			b, err := parseMatchBool(val)
			if err != nil {
				return Match{}, fmt.Errorf("window: parse fullscreen %q: %w", val, err)
			}
			m.Fullscreen = &b
		case "visible", "visible-only":
			b, err := parseMatchBool(val)
			if err != nil {
				return Match{}, fmt.Errorf("window: parse visible-only %q: %w", val, err)
			}
			m.VisibleOnly = b
		default:
			return Match{}, fmt.Errorf("window: unknown match key %q", key)
		}
	}
	return m, nil
}

func tokenizeMatchSpec(spec string) ([]string, error) {
	var tokens []string
	var buf strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		tokens = append(tokens, buf.String())
		buf.Reset()
	}
	for _, r := range spec {
		switch {
		case escaped:
			buf.WriteRune(r)
			escaped = false
		case quote != 0:
			switch r {
			case '\\':
				escaped = true
			case quote:
				quote = 0
			default:
				buf.WriteRune(r)
			}
		default:
			switch r {
			case '"', '\'':
				quote = r
			case ' ', '\t', '\n', '\r', ',':
				flush()
			default:
				buf.WriteRune(r)
			}
		}
	}
	if escaped {
		buf.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("window: unterminated quote in match spec %q", spec)
	}
	flush()
	return tokens, nil
}

func parseMatchBool(v string) (bool, error) {
	if v == "" {
		return true, nil
	}
	switch strings.ToLower(v) {
	case "1", "t", "true", "yes", "on":
		return true, nil
	case "0", "f", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", v)
	}
}
