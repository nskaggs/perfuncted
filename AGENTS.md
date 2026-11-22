# Perfuncted issue campaign

When coordinating the owner's open GitHub issue campaign for this repository:

1. Work one issue at a time. Select the lowest-numbered open issue with a
   matching `plans/*issue-<number>-*implementation-plan.md`, fetch its current
   GitHub issue, then read and follow that plan in full. Treat issue status,
   plan, source, and tests as separate evidence; update the plan only for
   evidence-backed current behavior.
2. Hand implementation to an existing, clearly designated ChatGPT thread. If
   none exists, ask the owner to authorize creation of one before starting it.
   Give the implementer the exact issue and plan paths, scope, acceptance
   criteria, delivery policy, and next action. Keep one writer in the shared
   checkout; the coordinator and reviewer remain read-only while it writes.
3. After implementation, inspect the exact diff and run the plan's required
   checks. Require every applicable test and CI gate to pass, plus 100%
   statement coverage of new and issue-specific modified executable code.
   Measure and retain the coverage evidence; report any platform-only paths
   that cannot be exercised and do not call them covered.
4. Obtain a read-only review of the exact completed diff from a distinct
   ChatGPT thread. Resolve actionable findings, then re-run affected checks and
   coverage. The coordinator also independently reviews the source, tests,
   diff, and evidence.
5. Deliver only the issue's authorized changes on `main` under the repository's
   normal direct-to-main policy. Verify the commit on `origin/main`; close the
   GitHub issue only after the implementation, both reviews, required checks,
   coverage, and delivery are verified. Then continue to the next open issue.

The owner explicitly requires both reviews and the coverage gate for this
campaign, overriding the general GOV-06 default that independent model review
is optional. Follow the issue plan's supported-backend and authority limits;
report blockers instead of weakening acceptance.
