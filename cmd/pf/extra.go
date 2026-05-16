package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

var outputFormatFlag = "plain"

func outputCmd(openPF func(*cliConfig) func() (*perfuncted.Perfuncted, error), cfg *cliConfig) *cobra.Command {
	cmd := &cobra.Command{Use: "output", Short: "Output discovery and metadata"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List outputs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cfg)()
			if err != nil {
				return err
			}
			defer pf.Close()
			outs, err := pf.Output.List(cmd.Context())
			if err != nil {
				return err
			}
			switch strings.ToLower(outputFormatFlag) {
			case "plain":
				out := cmd.OutOrStdout()
				for _, o := range outs {
					fmt.Fprintf(out, "%s\t%s\tgeometry=%d,%d,%d,%d\tresolution=%dx%d\tscale=%d\n",
						o.Name, o.Backend, o.Geometry.X, o.Geometry.Y, o.Geometry.W, o.Geometry.H, o.ResolutionW, o.ResolutionH, o.Scale)
				}
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				for _, o := range outs {
					if err := enc.Encode(outputInfoJSON(o)); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("unknown output format %q", outputFormatFlag)
			}
			return nil
		},
	}
	list.Flags().StringVar(&outputFormatFlag, "output", "plain", "plain|json")
	cmd.AddCommand(list)
	return cmd
}

type scriptRunner struct {
	ctx                 context.Context
	pf                  *perfuncted.Perfuncted
	selectedWindowTitle string
	hasSelection        bool
}

func runCmd(root *cobra.Command, openPF func(*cliConfig) func() (*perfuncted.Perfuncted, error), cfg *cliConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run FILE",
		Short: "Run CLI commands from a script file or stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			var r io.Reader
			if path == "-" {
				r = cmd.InOrStdin()
			} else {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				r = f
			}
			pf, err := openPF(cfg)()
			if err != nil {
				return err
			}
			defer pf.Close()
			sr := &scriptRunner{ctx: cmd.Context(), pf: pf}
			return sr.run(r)
		},
	}
	return cmd
}

func (s *scriptRunner) run(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		if s.ctx != nil {
			if err := s.ctx.Err(); err != nil {
				return err
			}
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		toks, err := splitShell(line)
		if err != nil {
			return fmt.Errorf("script line %d: %w", lineNo, err)
		}
		if len(toks) == 0 {
			continue
		}
		if err := s.exec(lineNo, toks); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func splitShell(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote := byte(0)
	escaped := false
	tokenStarted := false
	emit := func() {
		if tokenStarted {
			out = append(out, cur.String())
			cur.Reset()
			tokenStarted = false
		}
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			tokenStarted = true
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			tokenStarted = true
			escaped = true
		case '\'', '"':
			tokenStarted = true
			if inQuote == 0 {
				inQuote = ch
				continue
			}
			if inQuote == ch {
				inQuote = 0
				continue
			}
			cur.WriteByte(ch)
		case ' ', '\t':
			if inQuote != 0 {
				tokenStarted = true
				cur.WriteByte(ch)
				continue
			}
			emit()
		default:
			tokenStarted = true
			cur.WriteByte(ch)
		}
	}
	if escaped {
		return nil, fmt.Errorf("trailing escape")
	}
	if inQuote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	emit()
	return out, nil
}

func (s *scriptRunner) exec(lineNo int, toks []string) error {
	if len(toks) == 0 {
		return nil
	}
	switch toks[0] {
	case "window":
		return s.execWindow(lineNo, toks[1:])
	case "input":
		return s.execInput(lineNo, toks[1:])
	case "screen":
		return s.execScreen(lineNo, toks[1:])
	case "output":
		return s.execOutput(lineNo, toks[1:])
	case "info":
		return s.execInfo(lineNo, toks[1:])
	default:
		return fmt.Errorf("script line %d: unknown command %q", lineNo, toks[0])
	}
}

func (s *scriptRunner) execWindow(lineNo int, toks []string) error {
	if len(toks) == 0 {
		return fmt.Errorf("script line %d: missing window subcommand", lineNo)
	}
	switch toks[0] {
	case "select":
		if len(toks) < 2 {
			return fmt.Errorf("script line %d: window select requires a match spec", lineNo)
		}
		match, err := parseWindowMatchArgs(toks[1:])
		if err != nil {
			return err
		}
		wins, err := collectWindowMatches(s.ctx, s.pf.Window.Manager, match)
		if err != nil {
			return err
		}
		if len(wins) == 0 {
			return windowNotFoundError(match)
		}
		s.selectedWindowTitle = wins[0].Title
		s.hasSelection = true
		return nil
	case "list":
		wins, err := s.pf.Window.List(s.ctx)
		if err != nil {
			return err
		}
		printWindowListPlain(wins)
		return nil
	case "active":
		t, err := s.pf.Window.ActiveTitle(s.ctx)
		if err != nil {
			return err
		}
		fmt.Println(t)
		return nil
	case "activate", "close", "minimize", "maximize", "fullscreen", "unfullscreen", "restore":
		title, err := s.windowTitleArg(toks[1:])
		if err != nil {
			return err
		}
		return s.runWindowAction(toks[0], title)
	case "move":
		title, x, y, err := s.parseWindowMoveArgs(toks[1:])
		if err != nil {
			return err
		}
		return s.pf.Window.Move(s.ctx, title, x, y)
	case "resize":
		title, w, h, err := s.parseWindowResizeArgs(toks[1:])
		if err != nil {
			return err
		}
		return s.pf.Window.Resize(s.ctx, title, w, h)
	case "find":
		match, err := parseWindowMatchArgs(toks[1:])
		if err != nil {
			return err
		}
		wins, err := collectWindowMatches(s.ctx, s.pf.Window.Manager, match)
		if err != nil {
			return err
		}
		if len(wins) == 0 {
			return windowNotFoundError(match)
		}
		printWindowPlain(wins[0])
		return nil
	default:
		return fmt.Errorf("script line %d: unsupported window subcommand %q", lineNo, toks[0])
	}
}

func (s *scriptRunner) windowTitleArg(args []string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	if s.hasSelection {
		return s.selectedWindowTitle, nil
	}
	return "", fmt.Errorf("window command requires a title or a prior window select")
}

func (s *scriptRunner) runWindowAction(action, title string) error {
	switch action {
	case "activate":
		return s.pf.Window.Activate(s.ctx, title)
	case "close":
		return s.pf.Window.CloseWindow(s.ctx, title)
	case "minimize":
		return s.pf.Window.Minimize(s.ctx, title)
	case "maximize":
		return s.pf.Window.Maximize(s.ctx, title)
	case "fullscreen":
		return s.pf.Window.Fullscreen(s.ctx, title)
	case "unfullscreen":
		return s.pf.Window.Unfullscreen(s.ctx, title)
	case "restore":
		return s.pf.Window.Restore(s.ctx, title)
	default:
		return fmt.Errorf("unknown window action %q", action)
	}
}

func (s *scriptRunner) parseWindowMoveArgs(args []string) (title string, x, y int, err error) {
	title = ""
	x = 0
	y = 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--title":
			i++
			if i >= len(args) {
				return "", 0, 0, fmt.Errorf("--title requires a value")
			}
			title = args[i]
		case "--x":
			i++
			if i >= len(args) {
				return "", 0, 0, fmt.Errorf("--x requires a value")
			}
			x, err = strconv.Atoi(args[i])
			if err != nil {
				return "", 0, 0, err
			}
		case "--y":
			i++
			if i >= len(args) {
				return "", 0, 0, fmt.Errorf("--y requires a value")
			}
			y, err = strconv.Atoi(args[i])
			if err != nil {
				return "", 0, 0, err
			}
		default:
			if title == "" {
				title = args[i]
			}
		}
	}
	if title == "" {
		title, err = s.windowTitleArg(nil)
	}
	return
}

func (s *scriptRunner) parseWindowResizeArgs(args []string) (title string, w, h int, err error) {
	w, h = 0, 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--title":
			i++
			if i >= len(args) {
				return "", 0, 0, fmt.Errorf("--title requires a value")
			}
			title = args[i]
		case "--w":
			i++
			if i >= len(args) {
				return "", 0, 0, fmt.Errorf("--w requires a value")
			}
			w, err = strconv.Atoi(args[i])
			if err != nil {
				return "", 0, 0, err
			}
		case "--h":
			i++
			if i >= len(args) {
				return "", 0, 0, fmt.Errorf("--h requires a value")
			}
			h, err = strconv.Atoi(args[i])
			if err != nil {
				return "", 0, 0, err
			}
		default:
			if title == "" {
				title = args[i]
			}
		}
	}
	if title == "" {
		title, err = s.windowTitleArg(nil)
	}
	return
}

func (s *scriptRunner) execInput(lineNo int, toks []string) error {
	if len(toks) == 0 {
		return fmt.Errorf("script line %d: missing input subcommand", lineNo)
	}
	switch toks[0] {
	case "type":
		var stdin bool
		var text string
		for i := 1; i < len(toks); i++ {
			switch toks[i] {
			case "--stdin":
				stdin = true
			default:
				if text == "" {
					text = toks[i]
				} else {
					text += " " + toks[i]
				}
			}
		}
		if stdin {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			text = string(b)
		}
		return s.pf.Input.Type(s.ctx, text)
	case "location":
		x, y, err := s.pf.Input.PointerLocation(s.ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%d,%d\n", x, y)
		return nil
	default:
		return fmt.Errorf("script line %d: unsupported input subcommand %q", lineNo, toks[0])
	}
}

func (s *scriptRunner) execScreen(lineNo int, toks []string) error {
	if len(toks) == 0 {
		return fmt.Errorf("script line %d: missing screen subcommand", lineNo)
	}
	switch toks[0] {
	case "grab":
		rect := "0,0,1920,1080"
		out := "/tmp/pf-grab.png"
		for i := 1; i < len(toks); i++ {
			switch toks[i] {
			case "--rect":
				i++
				if i >= len(toks) {
					return fmt.Errorf("--rect requires a value")
				}
				rect = toks[i]
			case "--out":
				i++
				if i >= len(toks) {
					return fmt.Errorf("--out requires a value")
				}
				out = toks[i]
			}
		}
		r, err := parseRect(rect)
		if err != nil {
			return err
		}
		return s.pf.Screen.CaptureRegion(s.ctx, r, out)
	case "hash":
		rect := "0,0,1920,1080"
		for i := 1; i < len(toks); i++ {
			if toks[i] == "--rect" {
				i++
				if i >= len(toks) {
					return fmt.Errorf("--rect requires a value")
				}
				rect = toks[i]
			}
		}
		r, err := parseRect(rect)
		if err != nil {
			return err
		}
		h, err := s.pf.Screen.GrabRegionHash(s.ctx, r)
		if err != nil {
			return err
		}
		fmt.Printf("%08x\n", h)
		return nil
	default:
		return fmt.Errorf("script line %d: unsupported screen subcommand %q", lineNo, toks[0])
	}
}

func (s *scriptRunner) execOutput(lineNo int, toks []string) error {
	if len(toks) == 0 || toks[0] != "list" {
		return fmt.Errorf("script line %d: unsupported output subcommand", lineNo)
	}
	wins, err := s.pf.Output.List(s.ctx)
	if err != nil {
		return err
	}
	for _, o := range wins {
		fmt.Printf("%s\t%s\t%d,%d,%d,%d\tscale=%d\tresolution=%dx%d\n", o.Name, o.Backend, o.Geometry.X, o.Geometry.Y, o.Geometry.W, o.Geometry.H, o.Scale, o.ResolutionW, o.ResolutionH)
	}
	return nil
}

func (s *scriptRunner) execInfo(lineNo int, toks []string) error {
	_ = lineNo
	_ = toks
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(buildInfoReport())
}

func outputInfoJSON(o output.Info) map[string]any {
	return map[string]any{
		"name":         o.Name,
		"backend":      o.Backend,
		"geometry":     o.Geometry,
		"resolution_w": o.ResolutionW,
		"resolution_h": o.ResolutionH,
		"scale":        o.Scale,
		"physical_w":   o.PhysicalW,
		"physical_h":   o.PhysicalH,
		"make":         o.Make,
		"model":        o.Model,
		"description":  o.Description,
		"primary":      o.Primary,
	}
}

type infoReport struct {
	Compositor   string                     `json:"compositor"`
	Environment  map[string]string          `json:"environment"`
	Probes       map[string][]probe.Result  `json:"probes"`
	Capabilities map[string]capabilityEntry `json:"capabilities"`
}

type capabilityEntry struct {
	Supported bool   `json:"supported"`
	Backend   string `json:"backend,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func buildInfoReport() infoReport {
	envVars := map[string]string{
		"WAYLAND_DISPLAY":     os.Getenv("WAYLAND_DISPLAY"),
		"DISPLAY":             os.Getenv("DISPLAY"),
		"XDG_CURRENT_DESKTOP": os.Getenv("XDG_CURRENT_DESKTOP"),
		"XDG_RUNTIME_DIR":     os.Getenv("XDG_RUNTIME_DIR"),
	}
	kind := compositor.Detect()
	caps := map[string]capabilityEntry{}

	screenProbes := screen.Probe()
	windowProbes := window.Probe()
	inputProbes := input.Probe()
	outputProbes := output.ProbeRuntime(env.Current())

	caps["screen"] = capabilityFromProbes(screenProbes)
	caps["window"] = capabilityFromProbes(windowProbes)
	caps["input"] = capabilityFromProbes(inputProbes)
	caps["output"] = capabilityFromProbes(outputProbes)
	return infoReport{
		Compositor:  kind.String(),
		Environment: envVars,
		Probes: map[string][]probe.Result{
			"screen": screenProbes,
			"window": windowProbes,
			"input":  inputProbes,
			"output": outputProbes,
		},
		Capabilities: caps,
	}
}

func capabilityFromProbes(results []probe.Result) capabilityEntry {
	for _, r := range results {
		if r.Selected {
			return capabilityEntry{Supported: r.Available, Backend: r.Name, Reason: r.Reason}
		}
	}
	if len(results) > 0 {
		return capabilityEntry{Supported: results[0].Available, Backend: results[0].Name, Reason: results[0].Reason}
	}
	return capabilityEntry{}
}
