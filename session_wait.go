package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"github.com/nskaggs/perfuncted/v2/window"
)

const (
	// waitEvaluateFailureLimit bounds consecutive condition-evaluation errors
	// before a wait gives up. Conditions query live window-manager state, and on
	// slow or loaded hosts a single query can exceed its internal deadline (for
	// example sway IPC under CPU contention while another session tears down).
	// Such transient failures must not abort a long window wait, so they are
	// retried; sustained failure still fails with the last error attached.
	waitEvaluateFailureLimit = 30

	// waitEvaluateStreakBudget bounds how long one run of consecutive failures may
	// stay unanswered. The count alone is not a bound in the dimension that
	// matters: a wait driven by window or accessibility events evaluates far more
	// often than one driven by its poll interval, so the same count spans very
	// different durations, and a caller with a long deadline would keep waiting
	// on a source that has stopped answering.
	waitEvaluateStreakBudget = 2 * time.Second

	// waitEvaluateTotalLimit bounds failures across the whole wait. A backend
	// that alternates between answering and failing resets the consecutive count
	// every time it answers, so without a separate total it can be waited out
	// indefinitely while the caller is told nothing.
	waitEvaluateTotalLimit = 120
)

// waitEvaluateFailures tracks condition-evaluation failures so a wait can tell
// "the condition is not satisfied yet" apart from "the state cannot be read".
type waitEvaluateFailures struct {
	failFast    bool
	consecutive int
	total       int
	since       time.Time
	now         func() time.Time
}

func (f *waitEvaluateFailures) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// record accounts for one failed evaluation and reports whether the wait has
// tolerated failures long enough that continuing hides an unreadable state.
func (f *waitEvaluateFailures) record() (exhausted bool, consecutive, total int) {
	now := f.clock()
	if f.consecutive == 0 {
		f.since = now
	}
	f.consecutive++
	f.total++
	exhausted = f.consecutive >= waitEvaluateFailureLimit ||
		f.total >= waitEvaluateTotalLimit ||
		now.Sub(f.since) >= waitEvaluateStreakBudget
	return exhausted, f.consecutive, f.total
}

// reset records that the state was read successfully, ending the current run of
// failures. The wait-wide total deliberately survives, because a source that
// keeps failing between successful reads is still mostly unreadable.
func (f *waitEvaluateFailures) reset() {
	f.consecutive = 0
	f.since = time.Time{}
}

// isPermanentWaitError reports whether an evaluation error means the wait can
// never be satisfied and must abort immediately instead of being retried.
func isPermanentWaitError(err error) bool {
	// Locator identity/semantic failures are terminal. Generic existence
	// predicates may intentionally tolerate transient incomplete snapshots.
	return errors.Is(err, ErrSessionClosed) || errors.Is(err, ErrNilSession) ||
		errors.Is(err, ErrApplicationExited) || errors.Is(err, errLocatorUnverifiable) ||
		errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrManagedScopeChanged) ||
		errors.Is(err, ErrWindowIdentityChanged)
}

// Condition is an authoritative state check used by Session.Wait.
type Condition interface {
	evaluate(context.Context, *sessionCore) (bool, error)
	describe() string
	pollingOnly() bool
}

type conditionCapabilitySource interface {
	requiredCapabilities() ([]Capability, error)
}

type conditionPreflight interface {
	preflight() error
}

type conditionSessionValidator interface {
	validateSession(*sessionCore) error
}

type conditionFunc struct {
	name         string
	check        func(context.Context, *sessionCore) (bool, error)
	pollOnly     bool
	capabilities []Capability
	sessionCheck func(*sessionCore) error
	preflightErr error
}

func (c conditionFunc) evaluate(
	ctx context.Context,
	session *sessionCore,
) (bool, error) {
	return c.check(ctx, session)
}

func (c conditionFunc) describe() string {
	return c.name
}

func (c conditionFunc) pollingOnly() bool {
	return c.pollOnly
}

func (c conditionFunc) requiredCapabilities() ([]Capability, error) {
	return slices.Clone(c.capabilities), c.preflightErr
}

func (c conditionFunc) validateSession(session *sessionCore) error {
	if c.sessionCheck != nil {
		return c.sessionCheck(session)
	}
	return nil
}

func (c conditionFunc) preflight() error {
	if c.preflightErr != nil {
		return c.preflightErr
	}
	if c.check == nil {
		return fmt.Errorf("perfuncted: %w: nil wait predicate", ErrInvalidArgument)
	}
	return nil
}

// Predicate creates a polling-only condition for arbitrary caller state.
func Predicate(
	name string,
	check func(context.Context) (bool, error),
) Condition {
	var preflightErr error
	if check == nil {
		preflightErr = fmt.Errorf("perfuncted: %w: nil wait predicate", ErrInvalidArgument)
	}
	return conditionFunc{
		name: name,
		check: func(ctx context.Context, _ *sessionCore) (bool, error) {
			if check == nil {
				return false, fmt.Errorf("perfuncted: %w: nil wait predicate", ErrInvalidArgument)
			}
			return check(ctx)
		},
		pollOnly:     true,
		preflightErr: preflightErr,
	}
}

func sessionCondition(
	name string,
	check func(context.Context, *sessionCore) (bool, error),
	capabilities ...Capability,
) Condition {
	return conditionFunc{
		name:         name,
		check:        check,
		capabilities: slices.Clone(capabilities),
	}
}

func sessionConditionWithValidation(
	name string,
	check func(context.Context, *sessionCore) (bool, error),
	validate func(*sessionCore) error,
	capabilities ...Capability,
) Condition {
	return conditionFunc{
		name:         name,
		check:        check,
		capabilities: slices.Clone(capabilities),
		sessionCheck: validate,
	}
}

type capabilityCondition struct {
	condition    Condition
	capabilities []Capability
}

func (c capabilityCondition) evaluate(ctx context.Context, session *sessionCore) (bool, error) {
	if c.condition == nil {
		return false, fmt.Errorf("perfuncted: %w: nil wait condition", ErrInvalidArgument)
	}
	return c.condition.evaluate(ctx, session)
}

func (c capabilityCondition) describe() string {
	if c.condition == nil {
		return "invalid wait condition"
	}
	return c.condition.describe()
}

func (c capabilityCondition) pollingOnly() bool {
	return c.condition == nil || c.condition.pollingOnly()
}

func (c capabilityCondition) requiredCapabilities() ([]Capability, error) {
	child, err := conditionCapabilities(c.condition)
	if err != nil {
		return nil, err
	}
	return appendUniqueCapabilities(child, c.capabilities), nil
}

func (c capabilityCondition) preflight() error {
	if c.condition == nil {
		return fmt.Errorf("perfuncted: %w: nil wait condition", ErrInvalidArgument)
	}
	for _, capability := range c.capabilities {
		if !validCapability(capability) {
			return fmt.Errorf("perfuncted: %w: invalid wait capability", ErrInvalidArgument)
		}
	}
	return validateConditionPreflight(c.condition)
}

func (c capabilityCondition) validateSession(session *sessionCore) error {
	return validateConditionForSession(session, c.condition)
}

func (c capabilityCondition) validateActionTarget(root accessibility.NodeID, node accessibility.Node) error {
	if targetCondition, ok := c.condition.(actionTargetCondition); ok {
		return targetCondition.validateActionTarget(root, node)
	}
	return nil
}

// WithConditionCapabilities declares the session capabilities required by a
// caller-defined condition. Use it when a Predicate observes screen or other
// session state so action-and-wait APIs can reject unavailable observation
// before dispatch.
func WithConditionCapabilities(condition Condition, capabilities ...Capability) Condition {
	return capabilityCondition{condition: condition, capabilities: slices.Clone(capabilities)}
}

func conditionCapabilities(condition Condition) ([]Capability, error) {
	if condition == nil {
		return nil, fmt.Errorf("perfuncted: %w: nil wait condition", ErrInvalidArgument)
	}
	if source, ok := condition.(conditionCapabilitySource); ok {
		return source.requiredCapabilities()
	}
	return nil, nil
}

func appendUniqueCapabilities(existing, additions []Capability) []Capability {
	result := slices.Clone(existing)
	for _, capability := range additions {
		if !slices.Contains(result, capability) {
			result = append(result, capability)
		}
	}
	return result
}

func validateConditionPreflight(condition Condition) error {
	if condition == nil {
		return fmt.Errorf("perfuncted: %w: nil wait condition", ErrInvalidArgument)
	}
	if validator, ok := condition.(conditionPreflight); ok {
		if err := validator.preflight(); err != nil {
			return err
		}
	}
	switch typed := condition.(type) {
	case compositeCondition:
		if len(typed.conditions) == 0 {
			return fmt.Errorf("perfuncted: %w: empty composite wait condition", ErrInvalidArgument)
		}
		for _, child := range typed.conditions {
			if err := validateConditionPreflight(child); err != nil {
				return err
			}
		}
	case notCondition:
		return validateConditionPreflight(typed.child)
	}
	return nil
}

func validateWaitCondition(session *sessionCore, condition Condition, operation string) error {
	if err := validateConditionPreflight(condition); err != nil {
		return err
	}
	if err := validateConditionForSession(session, condition); err != nil {
		return err
	}
	capabilities, err := conditionCapabilities(condition)
	if err != nil {
		return err
	}
	for _, capability := range capabilities {
		if !validCapability(capability) {
			return fmt.Errorf("perfuncted: %w: invalid wait capability", ErrInvalidArgument)
		}
		if !session.Has(capability) {
			return &CapabilityError{Capability: capability, Operation: operation, Err: ErrUnavailable}
		}
	}
	return nil
}

func validateConditionForSession(session *sessionCore, condition Condition) error {
	if condition == nil {
		return fmt.Errorf("perfuncted: %w: nil wait condition", ErrInvalidArgument)
	}
	if validator, ok := condition.(conditionSessionValidator); ok {
		if err := validator.validateSession(session); err != nil {
			return err
		}
	}
	switch typed := condition.(type) {
	case compositeCondition:
		for _, child := range typed.conditions {
			if err := validateConditionForSession(session, child); err != nil {
				return err
			}
		}
	case notCondition:
		return validateConditionForSession(session, typed.child)
	}
	return nil
}

type compositeCondition struct {
	name       string
	conditions []Condition
	mode       string
}

func (c compositeCondition) describe() string {
	return c.name
}

func (c compositeCondition) pollingOnly() bool {
	// An event from any event-capable child can change the composite result.
	// Polling-only children still get checked on every event and on fallback
	// polls, so they must not suppress useful event wakeups from siblings.
	for _, child := range c.conditions {
		if child != nil && !child.pollingOnly() {
			return false
		}
	}
	return true
}

func (c compositeCondition) validateActionTarget(root accessibility.NodeID, node accessibility.Node) error {
	for _, child := range c.conditions {
		if targetCondition, ok := child.(actionTargetCondition); ok {
			if err := targetCondition.validateActionTarget(root, node); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c compositeCondition) requiredCapabilities() ([]Capability, error) {
	var capabilities []Capability
	for _, child := range c.conditions {
		childCapabilities, err := conditionCapabilities(child)
		if err != nil {
			return nil, err
		}
		capabilities = appendUniqueCapabilities(capabilities, childCapabilities)
	}
	return capabilities, nil
}

func (c compositeCondition) evaluate(
	ctx context.Context,
	session *sessionCore,
) (bool, error) {
	switch c.mode {
	case "all":
		for _, child := range c.conditions {
			if child == nil {
				return false, fmt.Errorf("perfuncted: %w: nil child condition", ErrInvalidArgument)
			}
			ok, err := child.evaluate(ctx, session)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	case "any":
		var evaluationErrors []error
		for _, child := range c.conditions {
			if child == nil {
				return false, fmt.Errorf("perfuncted: %w: nil child condition", ErrInvalidArgument)
			}
			ok, err := child.evaluate(ctx, session)
			if err != nil {
				// One unreadable proof must not hide an independently
				// satisfied alternative. Preserve every error if none
				// succeeds, so Wait can report an unreadable condition.
				evaluationErrors = append(evaluationErrors, err)
				continue
			}
			if ok {
				return true, nil
			}
		}
		return false, errors.Join(evaluationErrors...)
	default:
		return false, fmt.Errorf("perfuncted: unknown composite mode %q", c.mode)
	}
}

// All succeeds when every child condition succeeds.
func All(conditions ...Condition) Condition {
	return compositeCondition{
		name:       "all",
		conditions: slices.Clone(conditions),
		mode:       "all",
	}
}

// Any succeeds when at least one child condition succeeds. Failed child
// observations do not mask independent successes; if none succeeds, all
// observation errors are preserved for the caller.
func Any(conditions ...Condition) Condition {
	return compositeCondition{
		name:       "any",
		conditions: slices.Clone(conditions),
		mode:       "any",
	}
}

type notCondition struct {
	child Condition
}

func (c notCondition) describe() string {
	if c.child == nil {
		return "not <nil>"
	}
	return "not " + c.child.describe()
}

func (c notCondition) pollingOnly() bool {
	return c.child != nil && c.child.pollingOnly()
}

func (c notCondition) validateActionTarget(root accessibility.NodeID, node accessibility.Node) error {
	if targetCondition, ok := c.child.(actionTargetCondition); ok {
		return targetCondition.validateActionTarget(root, node)
	}
	return nil
}

func (c notCondition) requiredCapabilities() ([]Capability, error) {
	return conditionCapabilities(c.child)
}

func (c notCondition) evaluate(
	ctx context.Context,
	session *sessionCore,
) (bool, error) {
	if c.child == nil {
		return false, fmt.Errorf("perfuncted: %w: nil child condition", ErrInvalidArgument)
	}
	ok, err := c.child.evaluate(ctx, session)
	return !ok, err
}

// Not succeeds when child does not.
func Not(child Condition) Condition {
	return notCondition{child: child}
}

// WindowExists succeeds when match identifies at least one window.
func WindowExists(match window.Match) Condition {
	return conditionFunc{
		name: "window exists " + match.String(),
		check: func(ctx context.Context, session *sessionCore) (bool, error) {
			windows, err := session.windows.List(ctx, match)
			return len(windows) > 0, err
		},
		capabilities: []Capability{CapabilityWindows},
		preflightErr: validateWindowMatch(match),
	}
}

// AccessibilityNodeExists succeeds when a fresh, structurally complete AT-SPI
// query returns at least one matching node. An incomplete query is an
// evaluation error: Session.Wait retries it as transient until one of the failure
// bounds in waitEvaluateFailures is reached, then returns the last error.
func AccessibilityNodeExists(root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) Condition {
	return sessionCondition("accessibility node exists", func(ctx context.Context, session *sessionCore) (bool, error) {
		nodes, err := session.accessibility.Find(ctx, root, query, opts)
		return len(nodes) > 0, err
	}, CapabilityAccessibility)
}

// AccessibilityFocused succeeds when AT-SPI reports a focused object.
func AccessibilityFocused(opts accessibility.SnapshotOptions) Condition {
	return sessionCondition("accessibility focused", func(ctx context.Context, session *sessionCore) (bool, error) {
		_, err := session.accessibility.Focused(ctx, opts)
		if errors.Is(err, accessibility.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	}, CapabilityAccessibility)
}

// AccessibilityStateContains succeeds when a matching accessible node has
// every requested state in a fresh, structurally complete query. An incomplete
// query is retried by Session.Wait as a transient evaluation error until one of
// the failure bounds in waitEvaluateFailures is reached. AT-SPI events only wake
// Session.Wait and are never treated as authoritative.
func AccessibilityStateContains(root accessibility.NodeID, query accessibility.Query, states []string, opts accessibility.SnapshotOptions) Condition {
	query.States = append([]string(nil), states...)
	return sessionCondition("accessibility state contains", func(ctx context.Context, session *sessionCore) (bool, error) {
		nodes, err := session.accessibility.Find(ctx, root, query, opts)
		return len(nodes) > 0, err
	}, CapabilityAccessibility)
}

// AccessibilityTextContains succeeds when an accessible node exposes the
// requested text. It is a convenience over AccessibilityNodeExists and keeps
// text matching bounded by SnapshotOptions.
func AccessibilityTextContains(root accessibility.NodeID, text string, opts accessibility.SnapshotOptions) Condition {
	return AccessibilityNodeExists(root, accessibility.Query{Text: text}, opts)
}

type accessibilityNodeChangedCondition struct {
	root     accessibility.NodeID
	baseline accessibility.Node
	opts     accessibility.SnapshotOptions
}

func (c accessibilityNodeChangedCondition) describe() string { return "accessibility node changed" }

func (c accessibilityNodeChangedCondition) pollingOnly() bool { return false }

func (c accessibilityNodeChangedCondition) requiredCapabilities() ([]Capability, error) {
	return []Capability{CapabilityAccessibility}, nil
}

func (c accessibilityNodeChangedCondition) preflight() error {
	if c.baseline.TextTruncated || len(c.baseline.Warnings) != 0 {
		return fmt.Errorf("perfuncted: %w: accessibility baseline is incomplete", ErrInvalidArgument)
	}
	if c.opts.VisibleOnly || len(c.opts.SkipRoles) != 0 {
		return fmt.Errorf("perfuncted: %w: filtered accessibility snapshots cannot prove target disappearance", ErrInvalidArgument)
	}
	if c.root.BusName == "" || c.root.ObjectPath == "" || c.root.Incarnation == 0 || c.baseline.ID.BusName == "" || c.baseline.ID.ObjectPath == "" || c.baseline.ID.Generation == 0 {
		return fmt.Errorf("perfuncted: %w: accessibility change condition requires a current root and baseline node", ErrInvalidArgument)
	}
	if c.root.BusName != c.baseline.ID.BusName || c.baseline.ID.Incarnation == 0 {
		return fmt.Errorf("perfuncted: %w: accessibility change condition requires a stable baseline identity", ErrInvalidArgument)
	}
	return nil
}

func (c accessibilityNodeChangedCondition) validateActionTarget(root accessibility.NodeID, current accessibility.Node) error {
	if !sameAccessibilityIdentity(root, c.root) {
		return fmt.Errorf("perfuncted: accessibility verification root differs from action scope: %w", ErrManagedScopeChanged)
	}
	if !sameAccessibilityIdentity(current.ID, c.baseline.ID) || !sameAccessibilityNodeState(current, c.baseline) {
		return fmt.Errorf("perfuncted: accessibility baseline target changed before dispatch: %w", ErrManagedScopeChanged)
	}
	return nil
}

func (c accessibilityNodeChangedCondition) evaluate(ctx context.Context, session *sessionCore) (bool, error) {
	if err := c.preflight(); err != nil {
		return false, err
	}
	opts := c.opts
	opts.Fresh = true
	snapshot, err := session.accessibility.Snapshot(ctx, c.root, opts)
	if err != nil {
		return false, err
	}
	if snapshot.Truncated || snapshot.ProviderErrors != 0 || len(snapshot.Warnings) != 0 {
		return false, fmt.Errorf("perfuncted: accessibility change observation is incomplete: %w", ErrUnavailable)
	}
	if !sameAccessibilityIdentity(snapshot.Root.ID, c.root) {
		return false, fmt.Errorf("perfuncted: accessibility root identity changed during verification: %w", ErrManagedScopeChanged)
	}
	for _, node := range snapshot.Nodes {
		if sameAccessibilityIdentity(node.ID, c.baseline.ID) {
			if node.TextTruncated || len(node.Warnings) != 0 {
				return false, fmt.Errorf("perfuncted: accessibility change target is incomplete: %w", ErrUnavailable)
			}
			return !sameAccessibilityNodeState(node, c.baseline), nil
		}
	}
	// A complete snapshot of the same root proves the baseline object is gone.
	return true, nil
}

// AccessibilityNodeChanged succeeds when a fresh complete snapshot shows that
// the same accessible object has a different observable state or is absent.
// Capture baseline before dispatch and retain it in this condition for one
// action's verification; errors never authorize replaying the action.
// When used as an action postcondition, the root must match the action scope.
func AccessibilityNodeChanged(root accessibility.NodeID, baseline accessibility.Node, opts accessibility.SnapshotOptions) Condition {
	// Own all mutable fields used by the comparison and preflight.
	baseline.Attributes = maps.Clone(baseline.Attributes)
	baseline.States = slices.Clone(baseline.States)
	baseline.Interfaces = slices.Clone(baseline.Interfaces)
	baseline.Actions = slices.Clone(baseline.Actions)
	baseline.Warnings = slices.Clone(baseline.Warnings)
	baseline.Value = cloneConditionValue(baseline.Value)
	baseline.Selection = cloneConditionValue(baseline.Selection)
	baseline.Table = cloneConditionValue(baseline.Table)
	baseline.Document = cloneConditionValue(baseline.Document)
	opts.SkipRoles = slices.Clone(opts.SkipRoles)
	return accessibilityNodeChangedCondition{root: root, baseline: baseline, opts: opts}
}

func cloneConditionValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func sameAccessibilityIdentity(current, baseline accessibility.NodeID) bool {
	return current.BusName == baseline.BusName && current.ObjectPath == baseline.ObjectPath && current.Incarnation == baseline.Incarnation
}

func sameAccessibilityNodeState(left, right accessibility.Node) bool {
	return sameAccessibilityNodeScalarState(left, right) && reflect.DeepEqual(left.Attributes, right.Attributes) &&
		slices.Equal(sortedStateCopy(left.States), sortedStateCopy(right.States)) &&
		slices.Equal(sortedStateCopy(left.Interfaces), sortedStateCopy(right.Interfaces)) &&
		reflect.DeepEqual(left.Value, right.Value) && reflect.DeepEqual(left.Selection, right.Selection) &&
		reflect.DeepEqual(left.Table, right.Table) && reflect.DeepEqual(left.Document, right.Document) && slices.Equal(left.Actions, right.Actions)
}

func sameAccessibilityNodeScalarState(left, right accessibility.Node) bool {
	return left.Name == right.Name && left.Description == right.Description && left.Role == right.Role && left.RoleID == right.RoleID &&
		left.Text == right.Text && left.TextTruncated == right.TextTruncated && left.Bounds == right.Bounds && left.HasBounds == right.HasBounds &&
		left.ChildCount == right.ChildCount && left.Focused == right.Focused && left.Visible == right.Visible && left.Showing == right.Showing &&
		left.Enabled == right.Enabled && left.Redacted == right.Redacted
}

func sortedStateCopy(states []string) []string {
	copy := slices.Clone(states)
	slices.Sort(copy)
	return copy
}

// WindowClosed succeeds when the stable window handle no longer exists.
func windowClosed(target *windowState) Condition {
	return sessionConditionWithValidation(
		"window closed",
		func(ctx context.Context, session *sessionCore) (bool, error) {
			if target == nil || target.id.session == nil {
				return false, ErrNilSession
			}
			if target.id.session != session {
				return false, fmt.Errorf(
					"perfuncted: window belongs to another session: %w",
					ErrInvalidArgument,
				)
			}
			if err := session.windows.checkAvailable("info"); err != nil {
				// Window capability is fixed for this session. An absent
				// manager can never establish that this native ID disappeared;
				// report unsupported observation immediately instead of
				// retrying an unavailable backend until the caller times out.
				if errors.Is(err, ErrUnavailable) {
					return false, errors.Join(ErrUnsupported, err)
				}
				return false, err
			}
			backend, ok := session.windows.backend().(interface {
				InfoByID(context.Context, string) (window.Info, error)
			})
			if !ok {
				return false, session.windows.operationError(
					"info",
					errors.Join(ErrUnsupported, window.ErrNotSupported),
				)
			}
			ctx, cancel := session.windows.backendContext(ctx, session.Timeouts().Medium)
			defer cancel()
			_, err := backend.InfoByID(
				ctx,
				target.id.native,
			)
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return false, err
			}
			if errors.Is(err, ErrWindowNotFound) {
				return true, nil
			}
			return false, session.windows.operationError("info", err)
		},
		func(session *sessionCore) error {
			if target == nil || target.id.session == nil {
				return ErrNilSession
			}
			if target.id.native == "" {
				return ErrInvalidArgument
			}
			if target.id.session != session {
				return fmt.Errorf("perfuncted: window belongs to another session: %w", ErrInvalidArgument)
			}
			return nil
		},
		CapabilityWindows,
	)
}

// WaitOption configures fallback polling.
type WaitOption func(*waitConfig) error

type waitConfig struct {
	failFast bool
	interval time.Duration
}

// WaitEvidence describes the bounded wakeups observed while a condition was
// evaluated. It is diagnostic evidence only; each wakeup still causes a fresh
// authoritative condition evaluation.
type WaitEvidence struct {
	Evaluations            int                  `json:"evaluations"`
	Wakeups                int                  `json:"wakeups"`
	PollWakeups            int                  `json:"pollWakeups"`
	WindowWakeups          int                  `json:"windowWakeups"`
	AccessibilityWakeups   int                  `json:"accessibilityWakeups"`
	ApplicationWakeups     int                  `json:"applicationWakeups"`
	LastWakeSource         string               `json:"lastWakeSource,omitempty"`
	LastAccessibilityEvent *accessibility.Event `json:"lastAccessibilityEvent,omitempty"`
}

// WaitEvery changes only the fallback polling cadence.
func WaitEvery(interval time.Duration) WaitOption {
	return func(config *waitConfig) error {
		if interval <= 0 {
			return fmt.Errorf("perfuncted: %w: wait interval must be positive", ErrInvalidArgument)
		}
		config.interval = interval
		return nil
	}
}

// Wait blocks until condition succeeds. Timeout and cancellation follow ctx;
// persistent observation failures are bounded, and terminal failures abort.
func (s *sessionCore) Wait(
	ctx context.Context,
	condition Condition,
	options ...WaitOption,
) error {
	_, err := s.WaitWithEvidence(ctx, condition, options...)
	return err
}

// WaitWithEvidence blocks until condition succeeds and returns bounded
// diagnostic evidence about the event-backed and polling wakeups observed.
// Events only wake the wait; the condition remains the authority.
func (s *sessionCore) WaitWithEvidence(
	ctx context.Context,
	condition Condition,
	options ...WaitOption,
) (WaitEvidence, error) {
	if s == nil {
		return WaitEvidence{}, ErrNilSession
	}
	if ctx == nil {
		return WaitEvidence{}, fmt.Errorf("perfuncted: wait: %w: nil context", ErrInvalidArgument)
	}
	if condition == nil {
		return WaitEvidence{}, fmt.Errorf("perfuncted: wait: %w: nil condition", ErrInvalidArgument)
	}
	config := waitConfig{interval: s.Timeouts().Poll}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&config); err != nil {
			return WaitEvidence{}, err
		}
	}
	if err := s.waitState(ctx); err != nil {
		return WaitEvidence{}, err
	}
	if err := validateConditionPreflight(condition); err != nil {
		return WaitEvidence{}, err
	}
	return s.waitWithEvidence(ctx, condition, config)
}

func (s *sessionCore) prepareWait(ctx context.Context, condition Condition, options []WaitOption, operation string) (waitConfig, error) {
	if s == nil {
		return waitConfig{}, ErrNilSession
	}
	if ctx == nil {
		return waitConfig{}, fmt.Errorf("perfuncted: %s: %w: nil context", operation, ErrInvalidArgument)
	}
	if condition == nil {
		return waitConfig{}, fmt.Errorf("perfuncted: %s: %w: nil condition", operation, ErrInvalidArgument)
	}
	if err := s.ensureOpen(); err != nil {
		return waitConfig{}, err
	}
	if err := ctx.Err(); err != nil {
		return waitConfig{}, err
	}
	if err := validateWaitCondition(s, condition, operation); err != nil {
		return waitConfig{}, err
	}
	config := waitConfig{interval: s.Timeouts().Poll}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&config); err != nil {
			return waitConfig{}, err
		}
	}
	return config, nil
}

func (s *sessionCore) waitWithEvidence(
	ctx context.Context,
	condition Condition,
	config waitConfig,
) (WaitEvidence, error) { //nolint:gocyclo // evaluation, wake, and bounded failure paths are intentionally explicit.
	var evidence WaitEvidence
	if err := s.ensureOpen(); err != nil {
		return evidence, err
	}
	ticker := time.NewTicker(config.interval)
	defer ticker.Stop()
	var evaluationFailures waitEvaluateFailures
	evaluationFailures.failFast = config.failFast
	for {
		if err := s.waitState(ctx); err != nil {
			return evidence, err
		}
		epoch := s.waitChanges()
		evidence.Evaluations++
		ok, err := s.evaluateWaitCondition(ctx, condition, &evaluationFailures)
		if err != nil {
			return evidence, err
		}
		if ok {
			// A custom condition can finish concurrently with shutdown or
			// cancel its own context; do not report an obsolete success.
			if err := s.waitState(ctx); err != nil {
				return evidence, err
			}
			return evidence, nil
		}
		if condition.pollingOnly() {
			epoch = nil
		}
		select {
		case <-ctx.Done():
			return evidence, ctx.Err()
		case <-s.ctx.Done():
			return evidence, ErrSessionClosed
		case <-waitEpochDone(epoch):
			if epoch != nil {
				evidence.recordWaitChange(epoch.change)
			}
		case <-ticker.C:
			evidence.PollWakeups++
		}
	}
}

func (s *sessionCore) evaluateWaitCondition(ctx context.Context, condition Condition, failures *waitEvaluateFailures) (bool, error) {
	ok, err := condition.evaluate(ctx, s)
	if err == nil {
		failures.reset()
		return ok, nil
	}
	if stateErr := s.waitState(ctx); stateErr != nil {
		return false, stateErr
	}
	if failures.failFast || isPermanentWaitError(err) {
		return false, fmt.Errorf("wait %s: %w", condition.describe(), err)
	}
	exhausted, consecutive, total := failures.record()
	if exhausted {
		return false, fmt.Errorf("wait %s: %d consecutive and %d total evaluation errors: %w",
			condition.describe(), consecutive, total, err)
	}
	// Transient query failure: keep polling until the caller's deadline; the
	// condition may still be satisfied later.
	return false, nil
}

func (e *WaitEvidence) recordWaitChange(change waitChange) {
	e.Wakeups++
	e.LastWakeSource = change.source
	switch change.source {
	case "window":
		e.WindowWakeups++
	case "accessibility":
		e.AccessibilityWakeups++
		if change.event != nil {
			event := *change.event
			e.LastAccessibilityEvent = &event
		}
	case "application":
		e.ApplicationWakeups++
	}
}

func (s *sessionCore) waitState(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.ensureOpen()
}

type invalidationHub struct {
	mu    sync.Mutex
	epoch *waitEpoch
}

// waitEpoch is immutable once notify closes done. Each waiter retains the
// evidence belonging to the exact broadcast epoch that woke it; a later
// notification cannot overwrite it before the waiter records the wake.
type waitEpoch struct {
	done   chan struct{}
	change waitChange
}

func newInvalidationHub() *invalidationHub {
	return &invalidationHub{epoch: &waitEpoch{done: make(chan struct{})}}
}

func (h *invalidationHub) subscribe() *waitEpoch {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.epoch
}

func (h *invalidationHub) notify(change waitChange) {
	h.mu.Lock()
	epoch := h.epoch
	epoch.change = cloneWaitChange(change)
	close(epoch.done)
	h.epoch = &waitEpoch{done: make(chan struct{})}
	h.mu.Unlock()
}

func cloneWaitChange(change waitChange) waitChange {
	if change.event != nil {
		event := *change.event
		change.event = &event
	}
	return change
}

type waitChange struct {
	source string
	event  *accessibility.Event
}

type windowChangeSource interface {
	WindowChanges() <-chan struct{}
}

func (s *sessionCore) waitChanges() *waitEpoch { //nolint:gocyclo // one hub owns independent window and accessibility sources.
	s.hubOnce.Do(func() {
		hub := newInvalidationHub()
		s.hubMu.Lock()
		s.hub = hub
		s.hubMu.Unlock()
		if s.windows != nil && !util.IsNil(s.windows.backend()) {
			if source, ok := s.windows.backend().(windowChangeSource); ok {
				if changes := source.WindowChanges(); changes != nil {
					go func() {
						for {
							select {
							case <-s.ctx.Done():
								return
							case _, ok := <-changes:
								if !ok {
									return
								}
								hub.notify(waitChange{source: "window"})
							}
						}
					}()
				}
			}
		}
		if s.accessibility != nil && !util.IsNil(s.accessibility.backend) {
			var openEvents func(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error)
			if owner, ok := s.accessibility.backend.(*accessibilityBackendOwner); ok {
				openEvents = owner.EventsAcrossReopens
			} else if source, ok := s.accessibility.backend.(accessibility.EventSource); ok {
				openEvents = source.Events
			}
			if openEvents != nil {
				go func() {
					events, err := openEvents(s.ctx, accessibility.EventOptions{Buffer: 128})
					if err != nil || events == nil {
						return
					}
					for {
						select {
						case <-s.ctx.Done():
							return
						case event, ok := <-events:
							if !ok {
								return
							}
							hub.notify(waitChange{source: "accessibility", event: &event})
						}
					}
				}()
			}
		}
	})
	s.hubMu.RLock()
	hub := s.hub
	s.hubMu.RUnlock()
	if hub == nil {
		return nil
	}
	return hub.subscribe()
}

func (s *sessionCore) notifyWaiters() {
	if s == nil {
		return
	}
	s.hubMu.RLock()
	hub := s.hub
	s.hubMu.RUnlock()
	if hub == nil {
		return
	}
	hub.notify(waitChange{source: "application"})
}

func waitEpochDone(epoch *waitEpoch) <-chan struct{} {
	if epoch == nil {
		return nil
	}
	return epoch.done
}

// WindowClosed returns a condition bound to the supplied target's current
// private identity, rather than to a caller-overwritable wrapper.
func WindowClosed(target *Window) Condition { return windowClosed(target.privateState()) }

// WaitFailFast aborts on the first observation error instead of retrying
// transient failures. Use when continuous authoritative observation is required.
func WaitFailFast() WaitOption {
	return func(config *waitConfig) error {
		config.failFast = true
		return nil
	}
}
