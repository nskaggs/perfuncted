//go:build integration && linux

package flatpaktest

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Flatpak export step may reject SVG when the runner lacks SVG icon
// sniffing support. Ship a self-contained PNG whose content and target
// layout are verified before a multi-minute bundle build.
func TestFlatpakExportIconsArePortablePNG(t *testing.T) {
	repo := mustRepoRoot(t)
	manifest, err := os.ReadFile(filepath.Join(repo, "io.github.nskaggs.perfuncted.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{128} {
		name := "io.github.nskaggs.perfuncted-" + fmt.Sprint(size) + ".png"
		data, err := os.Open(filepath.Join(repo, "flatpak", name))
		if err != nil {
			t.Fatal(err)
		}
		image, err := png.Decode(data)
		_ = data.Close()
		if err != nil {
			t.Fatalf("invalid PNG asset %s: %v", name, err)
		}
		if image.Bounds().Dx() != size || image.Bounds().Dy() != size {
			t.Errorf("icon %s dimensions %v, want %dx%d", name, image.Bounds(), size, size)
		}
		expected := "share/icons/hicolor/" + fmt.Sprint(size) + "x" + fmt.Sprint(size) + "/apps/"
		if !strings.Contains(string(manifest), expected) {
			t.Errorf("manifest missing portable %s icon installation", expected)
		}
	}
	if strings.Contains(string(manifest), "scalable/apps/") || strings.Contains(string(manifest), "256x256/apps/") {
		t.Fatal("Flatpak icon export must use portable 128x128 PNG; larger icons fail older runner validators")
	}
}
