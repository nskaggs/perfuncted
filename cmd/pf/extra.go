package main

import (
	"github.com/nskaggs/perfuncted/v2/internal/sessionregistry"

	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/compositor"
	diagnostic "github.com/nskaggs/perfuncted/v2/internal/diagnostic"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/probe"
	"github.com/nskaggs/perfuncted/v2/internal/workflow"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/window"
)

var outputFormatFlag = "plain"

func outputCmd(openPF sessionOpener) *cobra.Command {
	cmd := &cobra.Command{Use: "output", Short: "Output discovery and metadata"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List outputs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			outs, err := pf.Outputs().List(cmd.Context())
			if err != nil {
				return err
			}
			switch strings.ToLower(outputFormatFlag) {
			case "plain":
				out := cmd.OutOrStdout()
				for _, o := range outs {
					if err := writeCLIOutput(out, "%s\t%s\tgeometry=%d,%d,%d,%d\tresolution=%dx%d\tscale=%s\n",
						o.Name, o.Backend, o.Geometry.X, o.Geometry.Y, o.Geometry.W, o.Geometry.H, o.ResolutionW, o.ResolutionH, outputScaleString(o)); err != nil {
						return err
					}
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
	lifecycle         *workflow.Lifecycle
	externalLifecycle bool
	ctx               context.Context //nolint:containedctx // runner owns command context
	in                io.Reader
	out               io.Writer
	pf                *perfuncted.Session
	selectedWindow    *perfuncted.Window
	sync              bool
	scriptFromStdin   bool
}

func validateControlledScriptFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("workflow controls require a regular script file")
	}
	return nil
}

func runCmd(
	openPF sessionOpener,
	cfg *cliConfig,
) *cobra.Command {
	var workflowID string
	cmd := &cobra.Command{
		Use:   "run FILE",
		Short: "Run CLI commands from a script file or stdin",
		Long: `Run CLI commands from a script file or stdin.

Use --workflow-id to expose same-user pause, resume, stop, and status controls:

  pf run task.pf --workflow-id task-1
  pf workflow pause task-1
  pf workflow status task-1 --output json
  pf workflow resume task-1
  pf workflow stop task-1

Pause takes effect between script commands; a command already in progress may
finish before status becomes paused. Stop cancels the run context and closes
its Session so owned input cleanup can finish. Stop cannot undo application
changes that already happened. Controlled runs require a script file, reject
input type --stdin and explicit key-down/key-up syntax; atomic key chords remain
available. A crash can make the live state unavailable until the stale private
control record is safely recovered by starting another run with the same ID.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			if workflowID != "" && path == "-" {
				return errors.New("workflow controls require a script file; stdin cannot be interrupted while waiting for the next line")
			}
			var r io.Reader
			if path == "-" {
				r = cmd.InOrStdin()
			} else {
				cleanPath := filepath.Clean(path)
				root, err := os.OpenRoot(filepath.Dir(cleanPath))
				if err != nil {
					return err
				}
				defer root.Close()
				flags := os.O_RDONLY
				if workflowID != "" {
					flags |= unix.O_NONBLOCK
				}
				f, err := root.OpenFile(filepath.Base(cleanPath), flags, 0)
				if err != nil {
					return err
				}
				defer f.Close()
				if workflowID != "" {
					if err := validateControlledScriptFile(f); err != nil {
						return err
					}
				}
				r = f
			}
			var lifecycle *workflow.Lifecycle
			var control *sessionregistry.WorkflowControl
			var err error
			runCtx := cmd.Context()
			if workflowID != "" {
				lifecycle = workflow.New(cmd.Context(), workflowID)
				control, err = sessionregistry.StartWorkflowControl(lifecycle)
				if err != nil {
					lifecycle.Cancel()
					return err
				}
				defer lifecycle.Cancel()
				runCtx = lifecycle.Context()
			}
			pf, err := openPF(runCtx)
			if err != nil {
				if lifecycle != nil {
					return errors.Join(lifecycle.Finish(err), control.Close())
				}
				return err
			}
			sr := &scriptRunner{
				lifecycle:         lifecycle,
				externalLifecycle: lifecycle != nil,
				ctx:               runCtx,
				in:                cmd.InOrStdin(),
				out:               cmd.OutOrStdout(),
				pf:                pf,
				sync:              cfg != nil && cfg.sync,
				scriptFromStdin:   path == "-",
			}
			runErr := sr.run(r)
			closeErr := pf.Close()
			if lifecycle != nil {
				finalErr := lifecycle.Finish(errors.Join(runErr, closeErr))
				return errors.Join(finalErr, control.Close())
			}
			return runErr
		},
	}
	cmd.Flags().StringVar(&workflowID, "workflow-id", "", "explicit ID for same-user workflow control")
	return cmd
}

func (s *scriptRunner) run(r io.Reader) (runErr error) {
	originalCtx := s.ctx
	if s.lifecycle == nil {
		s.lifecycle = workflow.New(s.ctx, "")
	}
	lifecycle := s.lifecycle
	s.ctx = lifecycle.Context()
	defer func() {
		if !s.externalLifecycle {
			runErr = lifecycle.Finish(runErr)
		}
		s.ctx = originalCtx
		s.lifecycle = nil
	}()
	// Use a single buffered reader for script lines and any subsequent
	// input type --stdin payload. A Scanner can prefetch the remaining stdin
	// into its private buffer, leaving io.ReadAll(s.in) with no bytes.
	reader := bufio.NewReaderSize(r, bufio.MaxScanTokenSize)
	if s.scriptFromStdin {
		s.in = reader
	}
	return workflow.Run(s.ctx, reader, lifecycle, s.exec)
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
		target, err := s.pf.Windows().Find(s.ctx, match)
		if err != nil {
			return err
		}
		s.selectedWindow = target
		return nil
	case "list":
		wins, err := collectWindowMatches(
			s.ctx,
			s.pf.Windows(),
			window.Match{},
		)
		if err != nil {
			return err
		}
		return printWindowListPlain(s.out, wins)
	case "active":
		t, err := s.pf.Windows().ActiveTitle(s.ctx)
		if err != nil {
			return err
		}
		return writeCLIMessage(s.out, t)
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
		if err := target.Move(s.ctx, x, y); err != nil {
			return err
		}
		return s.syncWindows()
	case "resize":
		title, w, h, err := s.parseWindowResizeArgs(toks[1:])
		if err != nil {
			return err
		}
		target, err := s.targetForTitle(title)
		if err != nil {
			return err
		}
		if err := target.Resize(s.ctx, w, h); err != nil {
			return err
		}
		return s.syncWindows()
	case "find":
		match, err := parseWindowMatchArgs(toks[1:])
		if err != nil {
			return err
		}
		wins, err := collectWindowMatches(s.ctx, s.pf.Windows(), match)
		if err != nil {
			return err
		}
		if len(wins) == 0 {
			return windowNotFoundError(match)
		}
		return printWindowPlain(s.out, wins[0])
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
		return findWindowByTitle(s.ctx, s.pf.Windows(), title)
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
	var err error
	switch action {
	case "activate":
		err = target.Activate(s.ctx)
	case "close":
		err = target.Close(s.ctx)
	case "minimize":
		err = target.Minimize(s.ctx)
	case "maximize":
		err = target.Maximize(s.ctx)
	case "fullscreen":
		err = target.Fullscreen(s.ctx)
	case "unfullscreen":
		err = target.Unfullscreen(s.ctx)
	case "restore":
		err = target.Restore(s.ctx)
	default:
		return fmt.Errorf("unknown window action %q", action)
	}
	if err != nil {
		return err
	}
	return s.syncWindows()
}

//nolint:dupl // Move and resize deliberately share the same CLI grammar and error contract.
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
			if strings.HasPrefix(args[i], "--") || title != "" {
				return "", 0, 0, fmt.Errorf("unknown window option %q", args[i])
			}
			title = args[i]
		}
	}
	return
}

//nolint:dupl // Move and resize deliberately share the same CLI grammar and error contract.
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
			if strings.HasPrefix(args[i], "--") || title != "" {
				return "", 0, 0, fmt.Errorf("unknown window option %q", args[i])
			}
			title = args[i]
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
		return s.execInputType(lineNo, toks)
	case "type-literal":
		if len(toks) != 2 {
			return fmt.Errorf("script line %d: input type-literal requires exactly one text argument", lineNo)
		}
		if err := s.pf.Input().TypeLiteral(s.ctx, toks[1]); err != nil {
			return err
		}
		return s.syncInput()
	case "location":
		x, y, err := s.pf.Input().PointerLocation(s.ctx)
		if err != nil {
			return err
		}
		return writeCLIOutput(s.out, "%d,%d\n", x, y)
	default:
		return fmt.Errorf("script line %d: unsupported input subcommand %q", lineNo, toks[0])
	}
}

func (s *scriptRunner) execInputType(lineNo int, toks []string) error {
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
		if s.externalLifecycle {
			return errors.New("input type --stdin is not available with workflow controls because stream reads cannot be interrupted")
		}
		b, err := io.ReadAll(s.in)
		if err != nil {
			return err
		}
		text = string(b)
	}
	if s.externalLifecycle {
		if err := input.ValidateWorkflowKeySend(s.ctx, text); err != nil {
			return fmt.Errorf("script line %d: workflow input is not safe for interruption: %w", lineNo, err)
		}
	}
	if err := s.pf.Input().Type(s.ctx, text); err != nil {
		return err
	}
	return s.syncInput()
}

func (s *scriptRunner) syncInput() error {
	if !s.sync {
		return nil
	}
	return s.pf.Input().Sync(s.ctx)
}

func (s *scriptRunner) syncWindows() error {
	if !s.sync {
		return nil
	}
	return s.pf.Windows().Sync(s.ctx)
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
			default:
				return fmt.Errorf("unknown screen grab option %q", toks[i])
			}
		}
		r, err := parseRect(rect)
		if err != nil {
			return err
		}
		return s.pf.Screen().CaptureRegion(s.ctx, r, out)
	case "hash":
		rect := "0,0,1920,1080"
		for i := 1; i < len(toks); i++ {
			if toks[i] != "--rect" {
				return fmt.Errorf("unknown screen hash option %q", toks[i])
			}
			i++
			if i >= len(toks) {
				return fmt.Errorf("--rect requires a value")
			}
			rect = toks[i]
		}
		r, err := parseRect(rect)
		if err != nil {
			return err
		}
		h, err := s.pf.Screen().GrabRegionHash(s.ctx, r)
		if err != nil {
			return err
		}
		return writeCLIOutput(s.out, "%08x\n", h)
	default:
		return fmt.Errorf("script line %d: unsupported screen subcommand %q", lineNo, toks[0])
	}
}

func (s *scriptRunner) execOutput(lineNo int, toks []string) error {
	if len(toks) == 0 || toks[0] != "list" {
		return fmt.Errorf("script line %d: unsupported output subcommand", lineNo)
	}
	wins, err := s.pf.Outputs().List(s.ctx)
	if err != nil {
		return err
	}
	for _, o := range wins {
		if err := writeCLIOutput(s.out, "%s\t%s\t%d,%d,%d,%d\tscale=%s\tresolution=%dx%d\n", o.Name, o.Backend, o.Geometry.X, o.Geometry.Y, o.Geometry.W, o.Geometry.H, outputScaleString(o), o.ResolutionW, o.ResolutionH); err != nil {
			return err
		}
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

func outputScaleString(info output.Info) string {
	if info.Scale != 0 {
		return strconv.Itoa(info.Scale)
	}
	if info.ScaleNumerator > 0 && info.ScaleDenominator > 0 {
		return fmt.Sprintf("%d/%d", info.ScaleNumerator, info.ScaleDenominator)
	}
	return "unknown"
}

func outputInfoJSON(o output.Info) map[string]any {
	return map[string]any{
		"name":              o.Name,
		"backend":           o.Backend,
		"geometry":          o.Geometry,
		"resolution_w":      o.ResolutionW,
		"resolution_h":      o.ResolutionH,
		"scale":             o.Scale,
		"scale_numerator":   o.ScaleNumerator,
		"scale_denominator": o.ScaleDenominator,
		"physical_w":        o.PhysicalW,
		"physical_h":        o.PhysicalH,
		"make":              o.Make,
		"model":             o.Model,
		"description":       o.Description,
		"primary":           o.Primary,
		"available":         o.Available,
		"reason":            o.Reason,
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
	sessionEnv := session.Env()
	runtime := env.FromEnviron(sessionEnv)
	envVars := diagnostic.Environment(sessionEnv)
	kind := compositor.DetectRuntime(runtime)
	caps := map[string]capabilityEntry{}
	probes := diagnostic.Probes(runtime)

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
		Compositor:   kind.String(),
		Target:       string(session.Target().Kind()),
		Environment:  envVars,
		Probes:       probes,
		Capabilities: caps,
	}
}
