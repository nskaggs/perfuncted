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

func outputCmd(openPF sessionOpener) *cobra.Command {
	cmd := &cobra.Command{Use: "output", Short: "Output discovery and metadata"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List outputs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			outs, err := pf.Outputs.List(cmd.Context())
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
	ctx            context.Context //nolint:containedctx // runner owns command context
	in             io.Reader
	out            io.Writer
	pf             *perfuncted.Session
	selectedWindow *perfuncted.Window
}

func runCmd(
	openPF sessionOpener,
) *cobra.Command {
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
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			sr := &scriptRunner{
				ctx: cmd.Context(),
				in:  cmd.InOrStdin(),
				out: cmd.OutOrStdout(),
				pf:  pf,
			}
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

//nolint:gocyclo // command dispatch mirrors the window subcommand surface
func (s *scriptRunner) execWindow(
	lineNo int,
	toks []string,
) error {
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
		target, err := s.pf.Windows.Find(s.ctx, match)
		if err != nil {
			return err
		}
		s.selectedWindow = target
		return nil
	case "list":
		wins, err := collectWindowMatches(
			s.ctx,
			s.pf.Windows,
			window.Match{},
		)
		if err != nil {
			return err
		}
		printWindowListPlain(s.out, wins)
		return nil
	case "active":
		t, err := s.pf.Windows.ActiveTitle(s.ctx)
		if err != nil {
			return err
		}
		fmt.Fprintln(s.out, t)
		return nil
	case "activate", "close", "minimize", "maximize", "fullscreen", "unfullscreen", "restore":
		target, err := s.windowTarget(toks[1:])
		if err != nil {
			return err
		}
		return s.runWindowAction(toks[0], target)
	case "move":
		title, x, y, err := s.parseWindowMoveArgs(toks[1:])
		if err != nil {
			return err
		}
		target, err := s.targetForTitle(title)
		if err != nil {
			return err
		}
		return target.Move(s.ctx, x, y)
	case "resize":
		title, w, h, err := s.parseWindowResizeArgs(toks[1:])
		if err != nil {
			return err
		}
		target, err := s.targetForTitle(title)
		if err != nil {
			return err
		}
		return target.Resize(s.ctx, w, h)
	case "find":
		match, err := parseWindowMatchArgs(toks[1:])
		if err != nil {
			return err
		}
		wins, err := collectWindowMatches(s.ctx, s.pf.Windows, match)
		if err != nil {
			return err
		}
		if len(wins) == 0 {
			return windowNotFoundError(match)
		}
		printWindowPlain(s.out, wins[0])
		return nil
	default:
		return fmt.Errorf("script line %d: unsupported window subcommand %q", lineNo, toks[0])
	}
}

func (s *scriptRunner) windowTarget(
	args []string,
) (*perfuncted.Window, error) {
	if len(args) > 0 {
		return s.targetForTitle(strings.Join(args, " "))
	}
	if s.selectedWindow != nil {
		return s.selectedWindow, nil
	}
	return nil, fmt.Errorf(
		"window command requires a title or a prior window select",
	)
}

func (s *scriptRunner) targetForTitle(
	title string,
) (*perfuncted.Window, error) {
	if title != "" {
		return findWindowByTitle(s.ctx, s.pf.Windows, title)
	}
	if s.selectedWindow != nil {
		return s.selectedWindow, nil
	}
	return nil, fmt.Errorf(
		"window command requires a title or a prior window select",
	)
}

func (s *scriptRunner) runWindowAction(
	action string,
	target *perfuncted.Window,
) error {
	switch action {
	case "activate":
		return target.Activate(s.ctx)
	case "close":
		return target.Close(s.ctx)
	case "minimize":
		return target.Minimize(s.ctx)
	case "maximize":
		return target.Maximize(s.ctx)
	case "fullscreen":
		return target.Fullscreen(s.ctx)
	case "unfullscreen":
		return target.Unfullscreen(s.ctx)
	case "restore":
		return target.Restore(s.ctx)
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
			b, err := io.ReadAll(s.in)
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
		fmt.Fprintf(s.out, "%d,%d\n", x, y)
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
		fmt.Fprintf(s.out, "%08x\n", h)
		return nil
	default:
		return fmt.Errorf("script line %d: unsupported screen subcommand %q", lineNo, toks[0])
	}
}

func (s *scriptRunner) execOutput(lineNo int, toks []string) error {
	if len(toks) == 0 || toks[0] != "list" {
		return fmt.Errorf("script line %d: unsupported output subcommand", lineNo)
	}
	wins, err := s.pf.Outputs.List(s.ctx)
	if err != nil {
		return err
	}
	for _, o := range wins {
		fmt.Fprintf(s.out, "%s\t%s\t%d,%d,%d,%d\tscale=%d\tresolution=%dx%d\n", o.Name, o.Backend, o.Geometry.X, o.Geometry.Y, o.Geometry.W, o.Geometry.H, o.Scale, o.ResolutionW, o.ResolutionH)
	}
	return nil
}

func (s *scriptRunner) execInfo(lineNo int, toks []string) error {
	_ = lineNo
	_ = toks
	enc := json.NewEncoder(s.out)
	enc.SetIndent("", "  ")
	return enc.Encode(buildInfoReport(s.pf))
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
	Target       string                     `json:"target"`
	Environment  map[string]string          `json:"environment"`
	Probes       map[string][]probe.Result  `json:"probes"`
	Capabilities map[string]capabilityEntry `json:"capabilities"`
}

type capabilityEntry struct {
	Requested  bool     `json:"requested"`
	Required   bool     `json:"required"`
	Supported  bool     `json:"supported"`
	Backend    string   `json:"backend,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Operations []string `json:"operations,omitempty"`
}

func buildInfoReport(session *perfuncted.Session) infoReport {
	envVars := environmentMap(session.Env())
	kind := compositor.Detect()
	caps := map[string]capabilityEntry{}

	screenProbes := screen.Probe()
	windowProbes := window.Probe()
	inputProbes := input.Probe()
	outputProbes := output.ProbeRuntime(env.Current())

	for _, status := range session.Capabilities() {
		reason := ""
		if status.Failure != nil {
			reason = status.Failure.Error()
		}
		caps[string(status.Capability)] = capabilityEntry{
			Requested:  status.Requested,
			Required:   status.Required,
			Supported:  status.Available,
			Backend:    status.Backend,
			Reason:     reason,
			Operations: status.Operations,
		}
	}
	return infoReport{
		Compositor:  kind.String(),
		Target:      string(session.Target().Kind()),
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

func environmentMap(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
