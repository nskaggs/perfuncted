package window

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
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

// Matcher is a compiled Match with cached normalized fields for repeated use.
//
// Compile a Match once when you expect to apply it to many windows, such as in
// polling loops or full-window scans.
type Matcher struct {
	Match

	titleContainsLower string
}

type matchCacheKey struct {
	TitleContains string
	TitleExact    string
	AppID         string
	Class         string
	PID           int32
	HasPID        bool
	ID            uint64
	HasID         bool
	Active        bool
	HasActive     bool
	Minimized     bool
	HasMinimized  bool
	Maximized     bool
	HasMaximized  bool
	Fullscreen    bool
	HasFullscreen bool
	VisibleOnly   bool
}

var compiledMatchCache sync.Map

// CompileMatch returns a reusable matcher with cached normalized text fields.
func CompileMatch(m Match) Matcher {
	key := matchCacheKeyFromMatch(m)
	if cached, ok := compiledMatchCache.Load(key); ok {
		return cached.(Matcher)
	}
	compiled := Matcher{
		Match:              m,
		titleContainsLower: strings.ToLower(m.TitleContains),
	}
	actual, _ := compiledMatchCache.LoadOrStore(key, compiled)
	return actual.(Matcher)
}

// Matches reports whether info satisfies m.
func (m Match) Matches(info Info) bool {
	if m.TitleContains == "" {
		return Matcher{Match: m}.matches(info)
	}
	return CompileMatch(m).matches(info)
}

// Matches reports whether info satisfies m.
func (m Matcher) Matches(info Info) bool {
	return m.matches(info)
}

func (m Matcher) matches(info Info) bool {
	if m.TitleContains != "" && !strings.Contains(strings.ToLower(info.Title), m.titleContainsLower) {
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

func matchCacheKeyFromMatch(m Match) matchCacheKey {
	key := matchCacheKey{
		TitleContains: m.TitleContains,
		TitleExact:    m.TitleExact,
		AppID:         m.AppID,
		Class:         m.Class,
		VisibleOnly:   m.VisibleOnly,
	}
	if m.PID != nil {
		key.HasPID = true
		key.PID = *m.PID
	}
	if m.ID != nil {
		key.HasID = true
		key.ID = *m.ID
	}
	if m.Active != nil {
		key.HasActive = true
		key.Active = *m.Active
	}
	if m.Minimized != nil {
		key.HasMinimized = true
		key.Minimized = *m.Minimized
	}
	if m.Maximized != nil {
		key.HasMaximized = true
		key.Maximized = *m.Maximized
	}
	if m.Fullscreen != nil {
		key.HasFullscreen = true
		key.Fullscreen = *m.Fullscreen
	}
	return key
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
			// key may be "title" (exact) or "title~" (substring). The trailing "~"
			// is stripped by TrimSuffix before the switch, so case "title~" here
			// would be unreachable — the HasSuffix check below distinguishes the two.
			if strings.HasSuffix(key, "~") {
				m.TitleContains = val
			} else {
				m.TitleExact = val
			}
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
		case "state":
			if err := applyMatchState(&m, val); err != nil {
				return Match{}, err
			}
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

func applyMatchState(m *Match, raw string) error {
	negated := strings.HasPrefix(raw, "-")
	name := strings.ToLower(strings.TrimPrefix(raw, "-"))
	on := !negated

	switch name {
	case "active":
		m.Active = boolPtr(on)
	case "minimized":
		m.Minimized = boolPtr(on)
	case "maximized":
		m.Maximized = boolPtr(on)
	case "fullscreen":
		m.Fullscreen = boolPtr(on)
	case "visible", "visible-only":
		m.VisibleOnly = on
	default:
		return fmt.Errorf("window: unknown state %q", raw)
	}
	return nil
}

func boolPtr(v bool) *bool {
	return &v
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
