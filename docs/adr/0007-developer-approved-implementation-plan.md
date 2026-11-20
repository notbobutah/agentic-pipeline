# ADR-0007: Developer approval of the implementation plan before any code is written

**Status:** Accepted · **Date:** 2026-11-20

## Context
Agents can produce large amounts of plausible code quickly. The most expensive failure mode is not a bug but **building the wrong thing**: a misread ticket, the wrong approach, the wrong repo, or scope creep. That failure is only discovered at PR review, after the spend and with a big diff to unpick. Developers also need to stay accountable for what is built in their area.

## Decision
- Coding sessions never start until the **sponsoring developer** (or an owning-team delegate) approves a specific, versioned implementation plan (`ap.work.plan.approved{planId, version, digest}`).
- The plan is structured: approach, repos, files, tests mapped to acceptance criteria, out-of-scope items, risks, open questions, estimates and a **scope envelope** (repos, path globs, forbidden paths).
- Plans with unanswered open questions cannot be approved. Revisions create new versions. Approval is bound to the digest.
- During implementation the **Scope Guard** enforces the envelope. A deviation (path outside the envelope, a new repo, test weakening, approach change) pauses the session and requires re-approval.
- A material change to the ticket's description or acceptance criteria supersedes the approved plan.
- The agent identity can never approve. Approval is not waivable in v1.

## Consequences
- One extra human touchpoint per ticket (typically minutes). It is the cheapest point to correct direction, and it improves first-pass PR acceptance.
- PR review becomes "does the code implement the approved plan?" rather than "what is this?".
- The plan + approval + envelope give a strong audit trail and a precise scope for security controls.
- Latency: work waits on the developer. Mitigated by notifications, a delegate mechanism and expiry back to the sponsor.

## Alternatives
- Code first, review at PR only (rejected: wasted spend, large wrong diffs, weak control).
- Approval only for "risky" tasks (deferred: needs a reliable risk classifier; candidate for a v2 org allow-list).
