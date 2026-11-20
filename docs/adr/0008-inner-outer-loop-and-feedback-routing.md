# ADR-0008: Inner/outer loop split and routing feedback to the owning coding session

**Status:** Proposed · **Date:** 2026-11-20

## Context
Coding agents need fast feedback to make progress, but only the full delivery pipeline (clean executors, full test suites, scans, integration environments) is authoritative. Separately, the delivery plane already has a Remediator that reacts to failures. Without clear ownership, two agents could try to fix the same failure on an agent-authored PR.

## Decision
- **Inner loop:** the coding agent builds and runs targeted tests in its dev sandbox, using commands from the repo's Build Plan (or `agentDevelopment.innerLoop` in the manifest). It is fast and non-authoritative. Its results are recorded as `ap.devsession.inner_loop.completed`.
- **Outer loop:** every push goes through the normal delivery pipeline. It has no agent shortcut and is the only source of truth for "green".
- **Feedback Router (H-WRK-07):** pipeline diagnoses, attributed integration failures, failed gates, and review comments on a **session-owned change** become `ap.devsession.feedback.received` for that session only. The Remediation Dispatcher ignores session-owned changes. `infra` failures and unrelated flaky tests are never routed to sessions.
- Feedback is structured (typed events, locations, log windows, failing diff), never raw logs.
- Sessions stop on no-progress (same failure signature twice), iteration and cost limits, and escalate with a summary.

## Consequences
- Fewer pipeline runs per change (most errors are fixed in the inner loop). The escape rate (inner green, outer red) becomes a key environment-parity metric.
- Exactly one agent owns any failing change.
- Requires a reliable change → session ownership index (from `ap.devsession.change.opened` and commit trailers).

## Alternatives
Outer loop only (slow and costly), or inner loop only (not trustworthy; violates "agents get no shortcut").
