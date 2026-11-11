package window

import (
	"strings"
	"testing"
)

func TestKWinWindowParserRejectsExplicitNullGeometry(t *testing.T) {
	for _, field := range []string{"x", "y", "width", "height"} {
		t.Run(field, func(t *testing.T) {
			data := `[{"id":"first","x":9,"y":7,"width":12,"height":15},{"id":"second","` + field + `":null}]`
			windows, err := parseKWinWindowList(data)
			if err == nil {
				t.Fatalf("accepted JSON null %s as coordinate zero: %+v", field, windows)
			}
			if !strings.Contains(err.Error(), field) {
				t.Fatalf("error does not identify malformed %s: %v", field, err)
			}
		})
	}
}

func TestKWinWindowParserAllowsOmittedGeometryAndExplicitZero(t *testing.T) {
	windows, err := parseKWinWindowList(`[{"id":"omitted"},{"id":"origin","x":0,"y":0,"width":0,"height":0}]`)
	if err != nil || len(windows) != 2 || windows[0].X != 0 || windows[1].W != 0 {
		t.Fatalf("valid zero geometry failed: %+v err=%v", windows, err)
	}
}
