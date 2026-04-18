package main

import (
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

func hyphenate(s string) string {
	if s == "" {
		return ""
	}
	var words []string
	var buf []rune
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		ch := r[i]
		if i > 0 && unicode.IsUpper(ch) && (unicode.IsLower(r[i-1]) || (i+1 < len(r) && unicode.IsLower(r[i+1]))) {
			if len(buf) > 0 {
				words = append(words, string(buf))
				buf = []rune{}
			}
		}
		buf = append(buf, unicode.ToLower(ch))
	}
	if len(buf) > 0 {
		words = append(words, string(buf))
	}
	return strings.Join(words, "-")
}

func uniq(xs []string) []string {
	m := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		if x == "" {
			continue
		}
		if !m[x] {
			m[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

var commonPrefixes = []string{
	"Mouse", "Key", "Grab", "Find", "WaitFor", "Wait", "Get", "Set", "List", "Active", "Activate", "Press", "Scroll", "Close",
}

func candidatesFromMethod(name string) []string {
	c := []string{}
	c = append(c, hyphenate(name))
	// plain lowercase
	c = append(c, strings.ToLower(name))

	// try stripping common prefixes
	for _, p := range commonPrefixes {
		if strings.HasPrefix(name, p) {
			s := strings.TrimPrefix(name, p)
			if s != "" {
				c = append(c, hyphenate(s))
				c = append(c, strings.ToLower(s))
			}
		}
	}

	// special-case Hash suffix -> allow plain "hash"
	if strings.HasSuffix(name, "Hash") {
		c = append(c, "hash")
		base := strings.TrimSuffix(name, "Hash")
		c = append(c, hyphenate(base))
	}

	return uniq(c)
}

func methodsForType(pkg *packages.Package, typeName string) ([]string, error) {
	obj := pkg.Types.Scope().Lookup(typeName)
	if obj == nil {
		return nil, fmt.Errorf("type %s not found in package %s", typeName, pkg.PkgPath)
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("%s is not a named type", typeName)
	}

	// Use pointer method set to be permissive (includes both pointer/value receivers)
	ms := types.NewMethodSet(types.NewPointer(named))
	set := map[string]bool{}
	for i := 0; i < ms.Len(); i++ {
		sel := ms.At(i)
		m := sel.Obj()
		// only exported
		if m.Exported() {
			set[m.Name()] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func main() {
	// map CLI group -> perfuncted bundle type
	bundleTypes := map[string]string{
		"screen":    "ScreenBundle",
		"input":     "InputBundle",
		"window":    "WindowBundle",
		"clipboard": "ClipboardBundle",
	}

	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes, Dir: "."}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fmt.Fprintln(os.Stderr, "loading packages:", err)
		os.Exit(2)
	}

	var perfPkg *packages.Package
	for _, p := range pkgs {
		if p.PkgPath == "github.com/nskaggs/perfuncted" {
			perfPkg = p
			break
		}
	}
	if perfPkg == nil {
		fmt.Fprintln(os.Stderr, "could not find perfuncted package in module")
		os.Exit(2)
	}

	methods := map[string][]string{}
	for grp, tname := range bundleTypes {
		m, err := methodsForType(perfPkg, tname)
		if err != nil {
			// If type not found, continue but warn
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			methods[grp] = []string{}
			continue
		}
		methods[grp] = m
	}

	// collect docs-cli commands
	docsFiles, err := filepath.Glob("docs-cli/pf_*.md")
	if err != nil {
		fmt.Fprintln(os.Stderr, "globbing docs-cli:", err)
		os.Exit(2)
	}
	docs := map[string]map[string]bool{}
	re := regexp.MustCompile(`^pf_([a-z0-9-]+)_(.+)\.md$`)
	for _, f := range docsFiles {
		base := filepath.Base(f)
		if base == "pf.md" || strings.HasPrefix(base, "pf_completion") || base == "pf_docs.md" {
			continue
		}
		m := re.FindStringSubmatch(base)
		if m == nil {
			// skip pf_input.md and other group pages
			continue
		}
		grp := m[1]
		// doc files use underscores for multi-word commands (e.g. pf_input_scroll_right.md).
		// Normalize to hyphens so it matches candidatesFromMethod output.
		cmd := strings.ReplaceAll(m[2], "_", "-")
		if docs[grp] == nil {
			docs[grp] = map[string]bool{}
		}
		docs[grp][cmd] = true
	}

	missingInAPI := []string{}
	missingInCLI := []string{}

	// Check docs -> API: every docs command should map to at least one method candidate
	for grp, cmds := range docs {
		// only enforce for known bundles
		if _, ok := bundleTypes[grp]; !ok {
			continue
		}
		mset := map[string]bool{}
		for _, mn := range methods[grp] {
			for _, c := range candidatesFromMethod(mn) {
				mset[c] = true
			}
		}
		for cmd := range cmds {
			if !mset[cmd] {
				missingInAPI = append(missingInAPI, fmt.Sprintf("docs-cli: %s %s", grp, cmd))
			}
		}
	}

	// Check API -> docs: every exported method should have at least one docs command
	for grp, mlist := range methods {
		if _, ok := bundleTypes[grp]; !ok {
			continue
		}
		for _, mn := range mlist {
			ok := false
			for _, c := range candidatesFromMethod(mn) {
				if docs[grp] != nil && docs[grp][c] {
					ok = true
					break
				}
			}
			if !ok {
				missingInCLI = append(missingInCLI, fmt.Sprintf("api-only: %s.%s", grp, mn))
			}
		}
	}

	if len(missingInAPI) == 0 && len(missingInCLI) == 0 {
		fmt.Println("Basic CLI/API sync check: OK")
		os.Exit(0)
	}

	fmt.Println("CLI/API sync issues detected:")
	if len(missingInAPI) > 0 {
		fmt.Println("\nCLI doc commands with no matching API method (possible doc-only CLI):")
		for _, s := range missingInAPI {
			fmt.Println("  -", s)
		}
	}
	if len(missingInCLI) > 0 {
		fmt.Println("\nExported API methods with no matching CLI doc (possible API-only):")
		for _, s := range missingInCLI {
			fmt.Println("  -", s)
		}
	}

	fmt.Println("\nThis is a best-effort check. Some commands map to multiple API calls or use different names.")
	fmt.Println("If these warnings are expected, whitelist them or improve mapping rules in scripts/verify_cli_api.go")
	os.Exit(1)
}
