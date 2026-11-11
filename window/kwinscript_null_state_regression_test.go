package window

import (
	"strings"
	"testing"
)

func TestKWinWindowListRejectsExplicitNullState(t *testing.T) {
	for _, field := range []string{
		"pid", "active", "minimized", "maximized", "fullscreen", "keep_above", "keep_below",
		"shaded", "shadeable", "on_all_desktops", "desktop_ids", "tiled",
	} {
		t.Run(field, func(t *testing.T) {
			data := `[{"id":"good","active":true},{"id":"bad","` + field + `":null}]`
			list, err := parseKWinWindowList(data)
			if err == nil {
				t.Fatalf("accepted null compositor state %s: %+v", field, list)
			}
			if !strings.Contains(err.Error(), field) {
				t.Fatalf("error must identify %s: %v", field, err)
			}
		})
	}
}

func TestKWinDesktopListRejectsExplicitNullCurrent(t *testing.T) {
	data := `[{"id":"first","position":1,"current":true},{"id":"second","position":2,"current":null}]`
	ds, err := parseKWinDesktops(data)
	if err == nil {
		t.Fatalf("accepted null desktop current flag: %+v", ds)
	}
}

func TestKWinStateAllowsOmittedFields(t *testing.T) {
	list, err := parseKWinWindowList(`[{"id":"first","active":true},{"id":"second"}]`)
	if err != nil || len(list) != 2 || !list[0].Active {
		t.Fatalf("omitted fields rejected: %+v err=%v", list, err)
	}
	ds, err := parseKWinDesktops(`[{"id":"first","position":1,"current":true},{"id":"second","position":2}]`)
	if err != nil || len(ds) != 2 {
		t.Fatalf("omitted current rejected: %+v err=%v", ds, err)
	}
}
