package diagnostic

import (
	"encoding/json"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type BuildInfo struct {
	GoVersion     string       `json:"go_version"`
	OS            string       `json:"os"`
	Arch          string       `json:"arch"`
	WorkspaceMode string       `json:"workspace_mode"`
	Module        string       `json:"module,omitempty"`
	ModuleVersion string       `json:"module_version,omitempty"`
	Revision      string       `json:"revision,omitempty"`
	RevisionTime  string       `json:"revision_time,omitempty"`
	Modified      bool         `json:"modified,omitempty"`
	Dependencies  []Dependency `json:"dependencies,omitempty"`
}

type Dependency struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
	Sum     string `json:"sum,omitempty"`
}

func CurrentBuildInfo() BuildInfo {
	workspaceMode := os.Getenv("GOWORK")
	switch workspaceMode {
	case "":
		workspaceMode = "default"
	case "off":
		// Preserve the diagnostic distinction for disabled workspace mode.
	default:
		// GOWORK may contain a private absolute path to go.work. A failure
		// bundle needs only the workspace mode, not its local filename.
		workspaceMode = "on"
	}
	info := BuildInfo{
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		WorkspaceMode: workspaceMode,
	}
	build, ok := debug.ReadBuildInfo()
	if !ok || build == nil {
		return info
	}
	info.Module = build.Path
	info.ModuleVersion = build.Main.Version
	info.Dependencies = make([]Dependency, 0, len(build.Deps))
	for _, dependency := range build.Deps {
		if dependency == nil {
			continue
		}
		info.Dependencies = append(info.Dependencies, Dependency{
			Path: dependency.Path, Version: dependency.Version, Sum: dependency.Sum,
		})
	}
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			info.Revision = setting.Value
		case "vcs.time":
			info.RevisionTime = setting.Value
		case "vcs.modified":
			info.Modified, _ = strconv.ParseBool(setting.Value)
		}
	}
	return info
}

func WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return WriteAtomic(path, data, 0o600)
}

func WritePNG(path string, img image.Image) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".perfuncted-png-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if util.IsNil(img) {
		_ = file.Close()
		return fmt.Errorf("diagnostic: no screenshot image to encode")
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

//nolint:unparam // Explicit file mode is part of the safe atomic write helper contract.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".perfuncted-atomic-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, perm); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func Labels(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	limit := min(len(values), MaxListItems)
	labels := make([]string, 0, limit)
	for _, value := range values[:limit] {
		labels = append(labels, Label(value))
	}
	return labels
}

func Label(value string) string {
	var label strings.Builder
	label.Grow(min(len(value), MaxLabelBytes))
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		if size == 0 || label.Len()+utf8.RuneLen(r) > MaxLabelBytes {
			break
		}
		label.WriteRune(r)
		value = value[size:]
	}
	return label.String()
}

func Timestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

const (
	MaxLabelBytes = 128
	MaxListItems  = 16
)
