package main

import (
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCLISyncCheckerValidatesRealRepositoryAndRejectsOutdatedDocs(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(root, filepath.Join(root, "docs-cli")); got != 0 {
		t.Fatalf("real CLI documentation has API discrepancies; exit=%d", got)
	}
	docRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(docRoot, "pf_screen_totally-unimplemented-operation.md"), []byte("# bogus command\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(root, docRoot); got != 1 {
		t.Fatalf("unimplemented CLI docs were accepted: exit=%d", got)
	}
	if got := verifyCLISync(root, t.TempDir()); got != 1 {
		t.Fatalf("missing documented API commands were accepted: exit=%d", got)
	}
}

func TestCLISyncCheckerNeverFallsBackToUnrelatedModule(t *testing.T) {
	if got := verifyCLISync(t.TempDir(), t.TempDir()); got != 2 {
		t.Fatalf("unrelated module unexpectedly accepted: exit=%d", got)
	}
}

// An invalid documentation glob must be a hard verification failure rather
// than silently interpreting the documentation set as empty.
func TestCLISyncCheckerRejectsMalformedDocumentationGlob(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(root, "[invalid-glob"); got != 2 {
		t.Fatalf("malformed documentation glob status = %d, want 2", got)
	}
}

// A declared manual CLI alias must not hide the disappearance of its API
// counterpart. A synthetic module isolates the check from the real bundle.
func TestCLISyncCheckerRejectsMissingManualBinding(t *testing.T) {
	module := t.TempDir()
	docs := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":    "module github.com/nskaggs/perfuncted/v2\n\ngo 1.23\n",
		"bundle.go": "package perfuncted\ntype ScreenBundle struct{}\ntype InputBundle struct{}\ntype WindowBundle struct{}\ntype ClipboardBundle struct{}\n",
	} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(docs, "pf_window_keep_above.md"), []byte("# Keep above\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(module, docs); got != 1 {
		t.Fatalf("missing manual WindowBundle.SetKeepAboveByID binding status = %d, want 1", got)
	}
}

// The CLI/API contract must fail closed if a bundle API disappears entirely.
// Otherwise an empty documentation directory could make a broken package pass.
func TestCLISyncCheckerRejectsMissingBundleTypes(t *testing.T) {
	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module github.com/nskaggs/perfuncted/v2\n\ngo 1.23\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "bundle.go"), []byte("package perfuncted\ntype ScreenBundle struct{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := verifyCLISync(module, t.TempDir()); got != 2 {
		t.Fatalf("missing InputBundle, WindowBundle and ClipboardBundle accepted: exit=%d, want 2", got)
	}
}

func TestUniqRemovesEmptyValuesAndSorts(t *testing.T) {
	got := uniq([]string{"second", "first", "second", "", "first", "third"})
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uniq = %v want %v", got, want)
	}
	if len(uniq(nil)) != 0 {
		t.Fatal("nil input should return an empty result")
	}
}

func TestCandidatesFromMethodPreserveHyphenatedAndAlternativeForms(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   []string
	}{
		{"MouseClick", []string{"mouse-click", "mouseclick", "mouse", "click"}},
		{"WaitForChange", []string{"wait-for-change", "waitforchange", "waitfor", "change"}},
		{"GrabRegionHash", []string{"grab-region-hash", "grabregionhash", "hash", "grab-region"}},
		{"List", []string{"list"}},
		{"CloseWindow", []string{"close-window", "closewindow", "close", "window"}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			got := candidatesFromMethod(tc.method)
			for _, want := range tc.want {
				found := false
				for _, v := range got {
					if v == want {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("%q should generate %q, got %v", tc.method, want, got)
				}
			}
			for i, v := range got {
				if v == "" {
					t.Fatalf("empty candidate for %q", tc.method)
				}
				if i > 0 && strings.Compare(got[i-1], v) >= 0 {
					t.Fatalf("not sorted+unique: %v", got)
				}
			}
		})
	}
}

func TestSkipMethodForCLISyncHonorsContextAndManualOperations(t *testing.T) {
	for _, method := range []string{"ClickContext", "WaitFor", "Wait", "Sync", "Close", "FindColor", "GetMultiplePixels"} {
		if !skipMethodForCLISync(method) {
			t.Fatalf("%q should be excluded", method)
		}
	}
	for _, method := range []string{"Click", "MouseMove", "Type", "ListWindows", "CloseWindow"} {
		if skipMethodForCLISync(method) {
			t.Fatalf("%q unexpectedly excluded", method)
		}
	}
}

func TestMethodsForTypeUsesExportedPointerMethodSet(t *testing.T) {
	pkg := types.NewPackage("example.com/test", "test")
	obj := types.NewTypeName(token.NoPos, pkg, "Fixture", nil)
	named := types.NewNamed(obj, types.NewStruct(nil, nil), nil)
	if duplicate := pkg.Scope().Insert(obj); duplicate != nil {
		t.Fatalf("duplicate type %v", duplicate)
	}
	receiver := types.NewVar(token.NoPos, pkg, "recv", types.NewPointer(named))
	signature := types.NewSignatureType(receiver, nil, nil, types.NewTuple(), types.NewTuple(), false)
	named.AddMethod(types.NewFunc(token.NoPos, pkg, "Open", signature))
	named.AddMethod(types.NewFunc(token.NoPos, pkg, "private", signature))
	wrapper := &packages.Package{Types: pkg, PkgPath: pkg.Path()}
	got, err := methodsForType(wrapper, "Fixture")
	if err != nil || !reflect.DeepEqual(got, []string{"Open"}) {
		t.Fatalf("exported methods=%v error=%v", got, err)
	}
	if _, err := methodsForType(wrapper, "Missing"); err == nil {
		t.Fatal("missing named type was accepted")
	}
	notNamed := types.NewVar(token.NoPos, pkg, "Var", types.Typ[types.Int])
	pkg.Scope().Insert(notNamed)
	if _, err := methodsForType(wrapper, "Var"); err == nil {
		t.Fatal("non-named variable was accepted")
	}
}
