//go:build gencli
// +build gencli

// Command generator: produce cobra command wrappers from perfuncted API.
// Usage: go run ./scripts/gen_cli.go
// It reads scripts/cli-mapping.yaml and docs-cli/ to avoid generating commands
// that already exist in the hand-written CLI docs.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
	"gopkg.in/yaml.v3"
)

// Mapping controls per-method overrides and skips.
type MethodMapping struct {
	Name       string `yaml:"name,omitempty"`
	Skip       bool   `yaml:"skip,omitempty"`
	Positional bool   `yaml:"positional,omitempty"`
	OutFlag    string `yaml:"out_flag,omitempty"`
}

type Mapping map[string]map[string]MethodMapping

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

var commonPrefixes = []string{
	"Mouse", "Key", "Grab", "Find", "WaitFor", "Wait", "Get", "Set", "List", "Active", "Activate", "Press", "Scroll", "Close",
}

func candidatesFromMethod(name string) []string {
	c := []string{}
	c = append(c, hyphenate(name))
	c = append(c, strings.ToLower(name))
	for _, p := range commonPrefixes {
		if strings.HasPrefix(name, p) {
			s := strings.TrimPrefix(name, p)
			if s != "" {
				c = append(c, hyphenate(s))
				c = append(c, strings.ToLower(s))
			}
		}
	}
	if strings.HasSuffix(name, "Hash") {
		c = append(c, "hash")
		base := strings.TrimSuffix(name, "Hash")
		c = append(c, hyphenate(base))
	}
	// uniq
	m := map[string]bool{}
	out := []string{}
	for _, x := range c {
		if x == "" {
			continue
		}
		if !m[x] {
			m[x] = true
			out = append(out, x)
		}
	}
	return out
}

func methodsForType(pkg *packages.Package, typeName string) ([]*types.Func, error) {
	obj := pkg.Types.Scope().Lookup(typeName)
	if obj == nil {
		return nil, fmt.Errorf("type %s not found in package %s", typeName, pkg.PkgPath)
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("%s is not a named type", typeName)
	}
	ms := types.NewMethodSet(types.NewPointer(named))
	out := []*types.Func{}
	for i := 0; i < ms.Len(); i++ {
		sel := ms.At(i)
		m := sel.Obj().(*types.Func)
		if m.Exported() {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func loadMapping(path string) (Mapping, error) {
	m := Mapping{}
	b, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func collectDocs() (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	files, err := filepath.Glob("docs-cli/pf_*.md")
	if err != nil {
		return nil, err
	}
	re := regexpForDocs()
	for _, f := range files {
		base := filepath.Base(f)
		if base == "pf.md" || strings.HasPrefix(base, "pf_completion") || base == "pf_docs.md" {
			continue
		}
		m := re.FindStringSubmatch(base)
		if m == nil {
			continue
		}
		grp := m[1]
		cmd := strings.TrimSuffix(m[2], ".md")
		if out[grp] == nil {
			out[grp] = map[string]bool{}
		}
		out[grp][cmd] = true
	}
	return out, nil
}

func regexpForDocs() *regexp.Regexp {
	// compile here to avoid importing regexp at top-level prematurely
	return regexp.MustCompile(`^pf_([a-z0-9-]+)_(.+)\\.md$`)
}

// Type helpers --------------------------------------------------------------

func isErrorT(t types.Type) bool {
	return t.String() == "error"
}

func isImageType(t types.Type) bool {
	return t.String() == "image.Image"
}

func isRectangleType(t types.Type) bool {
	return t.String() == "image.Rectangle"
}

func isDurationType(t types.Type) bool {
	return t.String() == "time.Duration"
}

func isStringType(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Kind() == types.String
}

func isIntLike(t types.Type) bool {
	s := t.String()
	if strings.HasPrefix(s, "int") || strings.HasPrefix(s, "uint") {
		return true
	}
	if b, ok := t.(*types.Basic); ok {
		switch b.Kind() {
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
			return true
		}
	}
	return false
}

func paramNameOrDefault(v *types.Var, idx int) string {
	if v == nil {
		return fmt.Sprintf("arg%d", idx)
	}
	n := v.Name()
	if n == "" || n == "_" {
		return fmt.Sprintf("arg%d", idx)
	}
	return n
}

// Generation ---------------------------------------------------------------

func main() {
	mapPath := "scripts/cli-mapping.yaml"
	mapping, err := loadMapping(mapPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load mapping:", err)
		os.Exit(2)
	}

	docs, err := collectDocs()
	if err != nil {
		fmt.Fprintln(os.Stderr, "collect docs:", err)
		os.Exit(2)
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

	bundleTypes := map[string]string{
		"screen":    "ScreenBundle",
		"input":     "InputBundle",
		"window":    "WindowBundle",
		"clipboard": "ClipboardBundle",
	}

	outBuf := &bytes.Buffer{}
	fmt.Fprintln(outBuf, "// Code generated by scripts/gen_cli.go; DO NOT EDIT.")
	fmt.Fprintln(outBuf, "package main")
	fmt.Fprintln(outBuf, "")

	imports := map[string]bool{
		"github.com/spf13/cobra":        true,
		"github.com/nskaggs/perfuncted": true,
		"fmt":                           true,
	}
	// We'll populate imports as we discover uses.

	generated := map[string][]string{}

	for grp, tname := range bundleTypes {
		methods, err := methodsForType(perfPkg, tname)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			continue
		}
		for _, m := range methods {
			mn := m.Name()
			// mapping skip?
			if mapping[grp] != nil {
				if mm, ok := mapping[grp][mn]; ok && mm.Skip {
					continue
				}
			}
			// skip if docs already have candidate name
			cands := candidatesFromMethod(mn)
			skipBecauseDocs := false
			for _, c := range cands {
				if docs[grp] != nil && docs[grp][c] {
					skipBecauseDocs = true
					break
				}
			}
			if skipBecauseDocs {
				continue
			}

			// analyze signature
			sig, ok := m.Type().(*types.Signature)
			if !ok {
				continue
			}
			params := sig.Params()
			results := sig.Results()

			// disallow context or function types
			unsupported := false
			paramInfos := []string{}
			for i := 0; i < params.Len(); i++ {
				p := params.At(i)
				t := p.Type()
				if isStringType(t) {
					paramInfos = append(paramInfos, "string")
				} else if isIntLike(t) {
					paramInfos = append(paramInfos, "int")
				} else if isRectangleType(t) {
					paramInfos = append(paramInfos, "rect")
				} else if isDurationType(t) {
					paramInfos = append(paramInfos, "duration")
				} else {
					unsupported = true
					break
				}
			}
			if unsupported {
				continue
			}

			// results check: need last to be error
			if results.Len() == 0 {
				// no returns — treat as simple no-output action
			} else if !isErrorT(results.At(results.Len() - 1).Type()) {
				continue
			}

			// determine produce-able output type
			produce := "none"
			if results.Len() >= 2 {
				first := results.At(0).Type()
				if isImageType(first) {
					produce = "image"
					imports["os"] = true
					imports["image/png"] = true
				} else if first.String() == "uint32" {
					produce = "uint32"
					imports["fmt"] = true
				} else if first.String() == "string" {
					produce = "string"
					imports["fmt"] = true
				} else if first.String() == "int" || strings.HasPrefix(first.String(), "int") {
					if results.Len() >= 2 && (results.At(1).Type().String() == "int" || strings.HasPrefix(results.At(1).Type().String(), "int")) {
						produce = "int-int"
						imports["fmt"] = true
					} else {
						produce = "int"
						imports["fmt"] = true
					}
				} else {
					// unsupported output type
					continue
				}
			} else if results.Len() == 1 {
				// only error — no output
				produce = "none"
			}

			// at this point method is supported; render it
			cliName := ""
			if mapping[grp] != nil {
				if mm, ok := mapping[grp][mn]; ok && mm.Name != "" {
					cliName = mm.Name
				}
			}
			if cliName == "" {
				cliName = hyphenate(mn)
			}

			var sb strings.Builder
			cmdVar := fmt.Sprintf("cmd_%s_%s", grp, strings.ReplaceAll(cliName, "-", "_"))
			// flags vars
			flagVars := []string{}
			argList := []string{}
			// build RunE body
			sb.WriteString(fmt.Sprintf("\n\t// %s: wrapper for perfuncted.%s\n", cliName, mn))
			// declare flag variables
			for i := 0; i < params.Len(); i++ {
				p := params.At(i)
				pname := paramNameOrDefault(p, i+1)
				t := paramInfos[i]
				var vname string
				if pname == "arg1" || pname == "arg2" || pname == "arg3" {
					vname = fmt.Sprintf("%s_%s", cmdVar, pname)
				} else {
					vname = fmt.Sprintf("%s_%s", cmdVar, pname)
				}
				switch t {
				case "string":
					flagVars = append(flagVars, fmt.Sprintf("var %s string", vname))
					argList = append(argList, vname)
					imports["fmt"] = true
				case "int":
					flagVars = append(flagVars, fmt.Sprintf("var %s int", vname))
					argList = append(argList, vname)
				case "rect":
					flagVars = append(flagVars, fmt.Sprintf("var %s string", vname))
					argList = append(argList, vname+"_rect")
					imports["fmt"] = true
				case "duration":
					flagVars = append(flagVars, fmt.Sprintf("var %s string", vname))
					argList = append(argList, vname+"_dur")
				default:
					unsupported = true
				}
			}
			if unsupported {
				continue
			}

			// start command variable
			// If this command produces an image, add an "out" flag variable so
			// the flags registration below can reference it when generating the
			// StringVar call.
			if produce == "image" {
				outFlag := "out"
				if mapping[grp] != nil {
					if mm, ok := mapping[grp][mn]; ok && mm.OutFlag != "" {
						outFlag = mm.OutFlag
					}
				}
				flagVars = append(flagVars, fmt.Sprintf("var %s_%s string", cmdVar, outFlag))
			}
			sb.WriteString("\t" + strings.Join(flagVars, "\n\t") + "\n")
			sb.WriteString(fmt.Sprintf("\t%s := &cobra.Command{\n", cmdVar))
			sb.WriteString(fmt.Sprintf("\t\tUse:   \"%s\",\n", cliName))
			sb.WriteString(fmt.Sprintf("\t\tShort: \"Auto-generated wrapper for perfuncted.%s\",\n", mn))
			// Args for simple positional single-string param
			if params.Len() == 1 && isStringType(params.At(0).Type()) {
				if mapping[grp] != nil {
					if mm, ok := mapping[grp][mn]; ok && mm.Positional {
						sb.WriteString("\t\tArgs:  cobra.ExactArgs(1),\n")
					}
				}
			}
			sb.WriteString("\t\tRunE: func(_ *cobra.Command, args []string) error {\n")
			sb.WriteString("\t\t\tpf, err := openPF()\n\t\t\tif err != nil { return err }\n\t\t\tdefer pf.Close()\n")
			// parse params
			for i := 0; i < params.Len(); i++ {
				p := params.At(i)
				pname := paramNameOrDefault(p, i+1)
				t := paramInfos[i]
				vname := fmt.Sprintf("%s_%s", cmdVar, pname)
				switch t {
				case "string":
					// positional?
					if params.Len() == 1 {
						if mapping[grp] != nil {
							if mm, ok := mapping[grp][mn]; ok && mm.Positional {
								sb.WriteString(fmt.Sprintf("\t\t\tvar %s string\n", vname))
								sb.WriteString(fmt.Sprintf("\t\t\t%s = args[0]\n", vname))
								continue
							}
						}
					}
					sb.WriteString(fmt.Sprintf("\t\t\t// flag %s (string)\n", vname))
				case "int":
					sb.WriteString(fmt.Sprintf("\t\t\t// flag %s (int) already parsed into var\n", vname))
				case "rect":
					// parse rect
					sb.WriteString(fmt.Sprintf("\t\t\tr_%d, err := parseRect(%s_%s)\n", i, cmdVar, pname))
					sb.WriteString("\t\t\tif err != nil { return err }\n")
				case "duration":
					sb.WriteString(fmt.Sprintf("\t\t\t%s_dur, err := parseDuration(%s_%s, 0)\n", vname, cmdVar, pname))
					sb.WriteString("\t\t\tif err != nil { return err }\n")
				}
			}

			// call and handle results
			callParams := []string{}
			rectIndex := 0
			for i := 0; i < params.Len(); i++ {
				p := params.At(i)
				pname := paramNameOrDefault(p, i+1)
				t := paramInfos[i]
				switch t {
				case "string":
					vname := fmt.Sprintf("%s_%s", cmdVar, pname)
					callParams = append(callParams, vname)
				case "int":
					vname := fmt.Sprintf("%s_%s", cmdVar, pname)
					callParams = append(callParams, vname)
				case "rect":
					callParams = append(callParams, fmt.Sprintf("r_%d", rectIndex))
					rectIndex++
				case "duration":
					vname := fmt.Sprintf("%s_%s_dur", cmdVar, pname)
					callParams = append(callParams, vname)
				}
			}

			// build call string
			recv := "pf"
			bundleField := map[string]string{"screen": "Screen", "input": "Input", "window": "Window", "clipboard": "Clipboard"}[grp]
			callStr := fmt.Sprintf("%s.%s(%s)", recv+"."+bundleField, mn, strings.Join(callParams, ", "))

			if produce == "image" {
				// img, err := pf.Screen.Grab(r)
				sb.WriteString(fmt.Sprintf("\t\t\timg, err := %s\n", callStr))
				sb.WriteString("\t\t\tif err != nil { return err }\n")
				outFlag := "out"
				if mapping[grp] != nil {
					if mm, ok := mapping[grp][mn]; ok && mm.OutFlag != "" {
						outFlag = mm.OutFlag
					}
				}
				sb.WriteString(fmt.Sprintf("\t\t\tout := %s_%s\n", cmdVar, outFlag))
				sb.WriteString(fmt.Sprintf("\t\t\tif out == \"\" { out = \"/tmp/pf-%s.png\" }\n", cliName))
				sb.WriteString(fmt.Sprintf("\t\t\tf, err := os.Create(out)\n"))
				sb.WriteString("\t\t\tif err != nil { return err }\n")
				sb.WriteString("\t\t\tdefer f.Close()\n")
				sb.WriteString(fmt.Sprintf("\t\t\tif err := png.Encode(f, img); err != nil { return err }\n"))
				sb.WriteString("\t\t\tfmt.Println(out)\n")
				sb.WriteString("\t\t\treturn nil\n")
			} else if produce == "uint32" {
				sb.WriteString(fmt.Sprintf("\t\t\th, err := %s\n", callStr))
				sb.WriteString("\t\t\tif err != nil { return err }\n")
				sb.WriteString("\t\t\tfmt.Printf(\"%08x\\n\", h)\n")
				sb.WriteString("\t\t\treturn nil\n")
			} else if produce == "string" {
				sb.WriteString(fmt.Sprintf("\t\t\tres, err := %s\n", callStr))
				sb.WriteString("\t\t\tif err != nil { return err }\n")
				sb.WriteString("\t\t\tfmt.Print(res)\n")
				sb.WriteString("\t\t\treturn nil\n")
			} else if produce == "int-int" {
				sb.WriteString(fmt.Sprintf("\t\t\tw, h, err := %s\n", callStr))
				sb.WriteString("\t\t\tif err != nil { return err }\n")
				sb.WriteString("\t\t\tfmt.Printf(\"%dx%d\\n\", w, h)\n")
				sb.WriteString("\t\t\treturn nil\n")
			} else if produce == "int" {
				sb.WriteString(fmt.Sprintf("\t\t\tres, err := %s\n", callStr))
				sb.WriteString("\t\t\tif err != nil { return err }\n")
				sb.WriteString("\t\t\tfmt.Printf(\"%d\\n\", res)\n")
				sb.WriteString("\t\t\treturn nil\n")
			} else {
				// error-only
				sb.WriteString(fmt.Sprintf("\t\t\tif err := %s; err != nil { return err }\n", callStr))
				sb.WriteString("\t\t\treturn nil\n")
			}

			sb.WriteString("\t\t},\n")
			sb.WriteString("\t}\n")

			// flags registration
			for i := 0; i < params.Len(); i++ {
				p := params.At(i)
				pname := paramNameOrDefault(p, i+1)
				vname := fmt.Sprintf("%s_%s", cmdVar, pname)
				t := paramInfos[i]
				switch t {
				case "string":
					sb.WriteString(fmt.Sprintf("\t%s.Flags().StringVar(&%s, \"%s\", \"\", \"%s\")\n", cmdVar, vname, pname, pname))
				case "int":
					sb.WriteString(fmt.Sprintf("\t%s.Flags().IntVar(&%s, \"%s\", 0, \"%s\")\n", cmdVar, vname, pname, pname))
				case "rect":
					sb.WriteString(fmt.Sprintf("\t%s.Flags().StringVar(&%s, \"rect\", \"0,0,1920,1080\", \"x0,y0,x1,y1\")\n", cmdVar, vname))
				case "duration":
					sb.WriteString(fmt.Sprintf("\t%s.Flags().StringVar(&%s, \"%s\", \"\", \"%s\")\n", cmdVar, vname, pname, pname))
				}
			}

			// if image output, add out flag
			if produce == "image" {
				outFlag := "out"
				if mapping[grp] != nil {
					if mm, ok := mapping[grp][mn]; ok && mm.OutFlag != "" {
						outFlag = mm.OutFlag
					}
				}
				sb.WriteString(fmt.Sprintf("\t%s.Flags().StringVar(&%s_%s, \"%s\", \"\", \"output path\")\n", cmdVar, cmdVar, outFlag, outFlag))
			}

			generated[grp] = append(generated[grp], sb.String())
		}
	}

	// Now write imports
	impList := []string{}
	for k := range imports {
		impList = append(impList, k)
	}
	sort.Strings(impList)
	fmt.Fprintln(outBuf, "import (")
	for _, imp := range impList {
		fmt.Fprintf(outBuf, "\t\"%s\"\n", imp)
	}
	fmt.Fprintln(outBuf, ")")
	fmt.Fprintln(outBuf, "")

	// Emit autogen functions
	for _, grp := range []string{"screen", "input", "window", "clipboard"} {
		funcName := fmt.Sprintf("autogen%sCommands", strings.Title(grp))
		fmt.Fprintf(outBuf, "func %s(openPF func() (*perfuncted.Perfuncted, error)) []*cobra.Command {\n", funcName)
		fmt.Fprintln(outBuf, "\tcmds := []*cobra.Command{}")
		for _, block := range generated[grp] {
			fmt.Fprintln(outBuf, block)
			// find command variable name to append
			// simple heuristic: first token "cmd_<grp>_<name>"
			lines := strings.Split(block, "\n")
			cmdVar := ""
			for _, L := range lines {
				L = strings.TrimSpace(L)
				// Accept either ":=&cobra.Command" or ":= &cobra.Command" spacing variants.
				if strings.HasPrefix(L, "cmd_") && strings.Contains(L, ":=") && strings.Contains(L, "cobra.Command") {
					parts := strings.SplitN(L, ":=", 2)
					cmdVar = strings.TrimSpace(parts[0])
					break
				}
			}
			if cmdVar != "" {
				fmt.Fprintf(outBuf, "\tcmds = append(cmds, %s)\n", cmdVar)
			}
		}
		fmt.Fprintln(outBuf, "\treturn cmds\n}")
	}

	// format and write
	src := outBuf.Bytes()
	fmtSrc, err := format.Source(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "format error:", err)
		// write unformatted for debugging
		ioutil.WriteFile("cmd/pf/autogen_gen.go", src, 0644)
		os.Exit(2)
	}
	if err := ioutil.WriteFile("cmd/pf/autogen_gen.go", fmtSrc, 0644); err != nil {
		fmt.Fprintln(os.Stderr, "write autogen:", err)
		os.Exit(2)
	}
	fmt.Println("wrote cmd/pf/autogen_gen.go")
}
