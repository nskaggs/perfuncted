package main

import (
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

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
