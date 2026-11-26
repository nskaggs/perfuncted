// Package check_module_consumer verifies the shipped module boundary.
package check_module_consumer

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/zip"
)

func TestPublishedModuleConsumer(t *testing.T) {
	if testing.Short() {
		t.Skip("module archive and downstream compile are an explicit release gate")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	const path = "github.com/nskaggs/perfuncted/v2"
	// Each invocation uses a different synthetic prerelease to avoid reusing a
	// previously cached module zip or altering the real release's identity.
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	version := "v2.999.0-consumer." + hex.EncodeToString(nonce[:])
	proxy := filepath.Join(tmp, "proxy", filepath.FromSlash(path), "@v")
	if err := os.MkdirAll(proxy, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, content []byte) {
		t.Helper()
		if err := os.WriteFile(name, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Use Go's module zip rules, not a local replace: nested modules and
	// excluded paths must be treated exactly as they are in published zips.
	archive, err := os.Create(filepath.Join(proxy, version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	zipErr := zip.CreateFromDir(archive, module.Version{Path: path, Version: version}, root)
	closeErr := archive.Close()
	if zipErr != nil || closeErr != nil {
		t.Fatalf("create module archive: %v; close: %v", zipErr, closeErr)
	}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(proxy, version+".mod"), mod)
	info, err := json.Marshal(struct {
		Version string
		Time    time.Time
	}{version, time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(proxy, version+".info"), info)
	consumer := filepath.Join(tmp, "consumer")
	if err := os.Mkdir(consumer, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(consumer, "go.mod"), []byte("module example.invalid/perfuncted-consumer\n\ngo 1.27.4\n\nrequire "+path+" "+version+"\n"))
	write(filepath.Join(consumer, "consumer.go"), []byte("package consumer\nimport (\n _ \""+path+"\"\n _ \""+path+"/input\"\n _ \""+path+"/pftest\"\n)\n"))
	localProxy := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(tmp, "proxy"))}).String()
	fallback, err := exec.Command("go", "env", "GOPROXY").Output()
	if err != nil {
		t.Fatal(err)
	}
	// Only the synthetic candidate bypasses the public checksum database.
	// Ordinary dependencies retain the normal proxy/checksum configuration.
	command := exec.Command("go", "test", "-mod=mod", "-run", "^$", "./...")
	command.Dir = consumer
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY="+localProxy+","+strings.TrimSpace(string(fallback)), "GONOPROXY=none", "GONOSUMDB="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("external module consumer failed: %v\n%s", err, output)
	}
}
