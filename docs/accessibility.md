# Accessibility automation

## Wait for an accessible control

Use a scoped semantic selector and an explicit timeout to wait for a control
to appear, disappear, receive focus, become visible or enabled, or change its
accessible value:

```bash
pf a11y wait --app Editor --role button --name Save --until enabled --timeout 5s
pf a11y wait --window "Editor" --role status bar --name Status --until value-changed --timeout 10s --json
```

`--until` accepts `present`, `absent`, `focused`, `unfocused`, `visible`,
`hidden`, `enabled`, `disabled`, and `value-changed`. Existing `--state` flags
remain initial selector filters. Once the first unique control is resolved,
the wait follows that same node identity while its state or value changes.
Every condition uses fresh complete AT-SPI snapshots. Accessibility
notifications only wake the wait; a new snapshot must confirm success. A
timeout returns `context.DeadlineExceeded` in Go and a nonzero CLI result.
Ambiguous matches, incomplete snapshots, unsupported value properties, and a
lost application/window scope produce errors. `--visible-only` cannot prove
absence or a hidden state and is rejected for those conditions.

For `value-changed`, the first complete observation of the uniquely matched
control is the baseline. The wait compares `AT-SPI Value.CurrentValue` when
available, otherwise complete non-redacted accessible text. It does not infer
changes from screenshots. `visible` follows the provider's AT-SPI VISIBLE
state; it does not claim that a control is unobscured or physically on screen.
Waits read accessibility data and do not request input or screenshot access.
Sensitive values remain redacted unless `--allow-sensitive` is selected.

## Invoke an action and verify its outcome

`pf a11y action` remains a one-shot operation when no expectation is supplied.
To wait for accessible confirmation, use `--expect-text` or
`--expect-state changed` with a positive timeout:

```bash
pf a11y action --app Editor --role button --name Save --expect-text Saved --timeout 5s --json
pf a11y action --app Editor --role button --name Save --expect-state changed --timeout 5s --json
```

Verified JSON includes the dispatch receipt, outcome proof, and wait evidence.
On a verification timeout it reports `verification-timeout`, keeps the
accepted dispatch receipt, and exits nonzero. A timeout never retries the
action. `changed` captures a complete baseline before dispatch and compares
the same accessible object identity afterward. Verification means the
condition was observed; it does not prove that the action caused the change.

The Go action-and-wait API accepts the existing window and accessibility
conditions. For a caller-defined screen condition, capture its baseline before
the action and declare the required screen capability so unavailable screen
observation fails before dispatch:

```go
baseline, err := session.Screen().GrabRegionHash(ctx, region)
if err != nil {
    return err
}
screenChanged := perfuncted.WithConditionCapabilities(
    perfuncted.Predicate("screen region changed", func(ctx context.Context) (bool, error) {
        current, err := session.Screen().GrabRegionHash(ctx, region)
        return current != baseline, err
    }),
    perfuncted.CapabilityScreen,
)
receipt, evidence, err := session.InvokeAccessibilityActionAndWait(
    ctx, root, query, "", accessibility.SnapshotOptions{}, screenChanged,
)
```

`WindowExists`, `WindowClosed`, and accessibility condition builders declare
their own observation capabilities. Caller-defined conditions should use
`WithConditionCapabilities` when they read session bundles.

The Go API uses the same bounded wait engine:

```go
locator := session.Accessibility().LocatorForApplication(
    accessibility.ApplicationFilter{Name: "Editor"},
    accessibility.Selector{Role: "button", Name: "Save"},
    accessibility.SnapshotOptions{},
)
node, evidence, err := locator.WaitForState(ctx, perfuncted.AccessibilityWaitEnabled, 5*time.Second)
```

If two Save buttons match, the wait returns an ambiguity error rather than
choosing one. If no unique target is available before the deadline, it returns
a timeout; if the selected application or window changes during the wait, it
returns `perfuncted.ErrManagedScopeChanged`. If the matched control disappears
or is replaced before its state is observed, Go returns
`perfuncted.ErrAccessibilityTargetChanged`.
