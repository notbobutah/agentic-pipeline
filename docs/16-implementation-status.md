# 16 — Implementation Status

> Last updated: 2026-11-24 · Code at `45412af`

The design (docs 01–15) describes the full multi-tenant system. This repo implements a **thin vertical slice** of it: the [demo](demo/README.md). This doc records what is built, what the demo stands in for, what has been proven on a real run, and what is still design only.

## 1. Summary

| Demo phase | Status | Evidence |
|------------|--------|----------|
| P0 Setup | ✅ Done | Both repos green on `main` (2026-11-20) |
| P1 Event core, intake, plan | ✅ Done | `b760737`. Real planner runs in the sandbox. |
| P2 Sessions + PRs | ✅ Done | `c7e78b9`. DEMO-1 opened `PGDataLayer#1` and `GoReactChat#7`. |
| P3 Feedback loop | ✅ Built · ⚠️ not yet exercised live | CI was green on the first push in DEMO-1, so no fix iteration ran (see §3) |
| P4 Changeset | ✅ Done | Frontend pinned to the data SHA, re-pinned after the data merge, both merged |
| P5 Polish | 🟡 Partial | `ap timeline`, per-card cost and the `/pipeline` replay exist. No recorded 15-minute run yet. |

## 2. What is built

About 5,400 lines: Go in `cmd/` and `internal/`, TypeScript in `runner/`.

| Package | Implements | Design handlers |
|---------|------------|-----------------|
| `cmd/ap` | CLI: `serve [--once] [--publish-feed]`, `card submit\|comment`, `plan show [-v N] [--diff]`, `approve`, `revise`, `reject`, `cancel`, `status`, `timeline`, `events`, `feed build\|publish` | — |
| `internal/event` | CloudEvents envelope, UUIDv7, SQLite append-only store, idempotent keyed publish, catalog gate, in-process dispatcher | H-PLT-01 |
| `internal/work` | Card ingest, comment parser (`/ap approve`, `/ap revise …`), eligibility and repo resolution, Implementation Planner, plan validation, plan approval bound to a digest, work-item state machine, activity-file reporter | H-WRK-01 … 05, H-WRK-10 |
| `internal/session` | Host-side git workspaces, Test Author → Coding agent sessions, Scope Guard (envelope, forbidden paths, no weakening of Test Author tests), Applier (commits with `Ap-*` trailers, push, draft PRs), GitHub poller, Feedback Router with a rules classifier, iteration cap and escalation, changeset coordinator (pinning, joint readiness, merge, deploy) | H-WRK-06 … 09 |
| `internal/agent` | Agent gateway: one Docker container per task, per-role turn and USD budgets, JSONL progress → telemetry, result validation | — |
| `internal/gh` | Thin `gh` / REST wrapper for PRs, checks, reviews, merges and workflow runs | — |
| `internal/feed` | JSON snapshots (index, per-card events, telemetry, plans) force-pushed to the orphan `feed` branch for GoReactChat's `/pipeline` page | — |
| `internal/config` | Loads `demo/config.yaml` | — |
| `runner/` | TypeScript runner on the Claude Agent SDK. Tool allow-lists per role, restricted shell, structured result. | — |
| `sandbox/` | `node:24` + git + runner image | — |

## 3. First end-to-end run: DEMO-1 (2026-11-20)

| | |
|---|---|
| Card | DEMO-1, product catalog page backed by PGDataLayer (data + web) |
| Duration | 13:20 → 15:22 UTC (~2 h, including about 1.5 h waiting for plan approval) |
| Events | 67 in the log, 29 distinct types |
| Plan | v1 approved as proposed (no revision) |
| Sessions | 2 (data, web) · 5 commits pushed · 4 inner-loop runs |
| CI | Green on the first push for both repos. **No `feedback.received` or `diagnosis` events.** |
| Changeset | Created → ready → both PRs merged by the sponsor (`PGDataLayer#1` 15:14, `GoReactChat#7` 15:17) |
| Deploy | `deploy dev` ran on the merge SHA and **failed** at "Smoke test through the ingress" ([run 44762579397](https://github.com/notbobutah/GoReactChat/actions/runs/44762579397)) |
| Agent cost | **$4.77** |

Not yet demonstrated live:
- **Outer-loop fix iteration (P3).** The code path exists. It needs a card whose first push goes red; the design forbids injecting fake failures.
- **Plan revision** (`ap revise` → v2) on a real run. It is covered by `TestFlow`.
- **Scope deviation** (`plan.deviation.detected`) on a real run. The guard is covered by `TestGuard`.
- DEMO-2 … DEMO-6 have not been run.

## 4. Known issues

| # | Issue | Where | Effect |
|---|-------|-------|--------|
| K1 | ✅ **Fixed 2026-11-24.** A failed deployment used to complete the work item: the `TDeployed` handler emitted `ap.work.completed` without checking `conclusion`. It now emits `ap.agent.escalated` + `ap.work.blocked` (`deploy-failed`). The poller keeps watching, and keys re-runs by attempt, so a successful re-run completes the item. Covered by `TestDeployOutcome`. | `internal/session/feedback.go` (`Coordinator`), `state.go` (`Fold`), `poller.go` | DEMO-1's log still records `ap.work.completed` from before the fix. Events are append-only, so the history is left as it happened. |
| K2 | The poll interval is **30 s**, not the documented 15 s, and requests don't use ETags. | `internal/session/poller.go` (default `Interval`) | Slower feedback. The rate-limit reasoning in demo §11 assumed 15 s with ETags. |
| K3 | The CI fix loop has no live evidence (§3). | — | P3 is unproven until a red check occurs on a real run. |

## 5. Demo stand-ins vs the design

| Design | Demo | Gap to close for v1 |
|--------|------|---------------------|
| Jira Cloud app + webhooks (05, 14 §4) | Card JSON + `ap card comment`. Reporter writes `demo/cards/<KEY>.activity.md`. | Jira adapter, identity mapping (Q17) |
| GitHub App webhooks + reconciler (ADR-0004) | Polling only, as user `RobatDE`, using `gh` credentials on the host | GitHub App, webhook ingress, raw archive |
| Kafka-compatible bus + schema registry (ADR-0001/0002, Q1, Q9) | SQLite `events` table + dispatcher. The gate is `demo/catalog.yaml` (~47 types). | Bus, partitioning, registry, projections |
| Durable workflows / Temporal (ADR-0003) | Event-sourced state machines rebuilt from the log | Durable orchestrator, timers, retries |
| Policy engine / OPA (09) | Plain Go, still emitting `ap.policy.decision.recorded` | Rego policies, per-org autonomy (Q11) |
| Executors + typed build-process events (12) | GitHub Actions. Check runs map to `ap.pipeline.run/job.completed` only. | Executors emitting the A–Q event categories |
| Ephemeral integration environments (15, ADR-0009) | Compose-in-job inside the consumer's CI, pinned to the producer SHA | Environment manager, failure attribution |
| Diagnoser agent (07) | Rules classifier on check name + log excerpt (`Method: "rules"`) | Agent fallback for unclassified failures |
| Multi-tenancy, fair share, DR (10) | Single tenant `demo` | All of 10 |
| Agent Gateway (07, 09) | Docker per task, budgets, Anthropic-only egress (best effort) | Enforced egress, audit, model routing (Q13) |

## 6. Tests

`go test ./...` covers:
- **event:** idempotent keyed publish, catalog gate, envelope shape.
- **work:** fixture plan validation, plan validation errors, component matching, and the full intake → plan → revise → approve flow, including a replay that must add zero events.
- **session:** test-assertion diffing and test picking, Scope Guard.

- **session:** deploy outcome: a failed deploy blocks and escalates, and a successful re-run completes the item (`TestDeployOutcome`).

There are no automated tests for the poller, Feedback Router or changeset readiness and merging.
