# Perfuncted issue campaign

When coordinating the owner's open GitHub issue campaign for this repository:

1. Work one issue at a time. Select the lowest-numbered open issue with a
   matching `plans/*issue-<number>-*implementation-plan.md`, fetch its current
   GitHub issue, then read and follow that plan in full. Treat issue status,
   plan, source, and tests as separate evidence; update the plan only for
   evidence-backed current behavior.
2. Hand implementation to an existing, clearly designated ChatGPT thread when
   one exists. The owner's issue-campaign request authorizes one new ChatGPT
   implementation thread per issue when none is designated, plus a distinct
   ChatGPT thread for review. Give each thread the exact issue and plan paths,
   scope, acceptance criteria, delivery policy, and next action. For a
   ChatGPT handoff, keep one stable `client_message_id`: `start` returns a
   durable host job, not a message-delivery receipt. Inspect the exact job
   until terminal. After a verified send, invoke `wait` with that same ID
   and the coordinator `parent_thread_id`, **omitting `receipt_file`** so
   the controller derives the authenticated receipt from its own ledger.
   Do not fabricate a receipt from the host-job snapshot. For a failed or
   uncertain send, reconcile the original claim without duplicate sends.
   Keep one writer in the checkout; coordinator and reviewer remain read-only.
3. After implementation, inspect the exact diff and run the plan's required
   checks. Require **100% of applicable tests and CI gates to pass**. Measure
   and retain statement coverage of new and issue-specific changed code,
   exercising significant branches and failure boundaries. Do not silently
   redefine "all tests pass" as a demand for 100% statement coverage, or
   report unexercised platform-only code as covered. If a plan independently
   requires a stricter metric, honor that explicit requirement.
4. Obtain a read-only review of the exact completed diff from a distinct
   ChatGPT thread. Resolve actionable findings, then re-run affected checks and
   coverage. The coordinator also independently reviews the source, tests,
   diff, and evidence.
5. Deliver only the issue's authorized changes on `main` under the repository's
   normal direct-to-main policy. Verify the commit on `origin/main`; close the
   GitHub issue as a housekeeping step only after implementation, reviews,
   required checks, coverage reporting, and delivery are verified. Record
   completion in the orchestrator's private plan-workstream manifest, then
   register **all** completed implementation and review ChatGPT chats for the
   existing guarded archiver. Verify archive receipts separately. Proceed
   to the next issue only when the preceding checkout and ownership are safe.

The owner explicitly requires both reviews and all applicable test/CI gates
for this campaign, overriding the general GOV-06 default that independent
model review is optional. Follow the issue plan's supported-backend and
platform authority limits; report blockers rather than weakening acceptance.
