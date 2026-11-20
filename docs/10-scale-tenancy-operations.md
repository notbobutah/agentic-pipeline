# 10 — Scale, Tenancy & Operations

## 1. Capacity model (initial targets)

| Dimension | Target | Design response |
|-----------|--------|-----------------|
| Webhook peak | 500/s (monorepo pushes, mass rebases, Renovate storms) | Stateless ingress, autoscaled; archive write ≤ 50 ms |
| Bus throughput | ~2–3 M events/day (tier B+S) | 64–128 partitions, keyed by repo; hot repos detected and handled with subject sub-partitioning |
| Jobs | 50k/day, 2k concurrent peak | Executor pools autoscaled on queue depth (`ap.executor.pool.capacity.changed`); warm pools for common images |
| Agent tasks | ~2–5k/day | Async, cold-latency class; budget-gated |
| Coding sessions | 200 concurrent, each 15 min – 4 h | Dev-sandbox pool (microVMs with persistent session volume), per-team concurrency caps, sessions suspend (snapshot) while waiting on CI or a human |
| Ephemeral environments | 100 concurrent | Baseline + overlay by default (cheap). Per-team caps. TTL reaper. Environments are kept on failure only for their TTL. |
| Work items | ~300–1,000/day across the org | One durable workflow each; mostly idle waiting on humans or CI |

## 2. Fairness & quotas

- **Hierarchical weighted fair queuing:** tenant → team → repo, within each priority class.
- **Quotas:** concurrent jobs, monthly compute minutes, agent spend. Exceeding a quota degrades service (queue, then L0 agent mode). It does not drop work.
- **Storm protection:** per-repo rate limits on `run.requested` (e.g. a Renovate burst of 200 PRs is admitted gradually at p3). The Internal Dependency Fan-out is capped per upstream release.

## 3. Observability

- **Tracing:** the W3C `traceparent` on every event. One trace per correlation, from webhook to deployment, with spans for handlers, jobs, steps and agent tool calls.
- **Golden signals per handler:** consumer lag, processing latency, error rate, DLQ depth, idempotent-skip rate.
- **Pipeline SLOs:**
  - webhook → `run.queued` p95 ≤ 5 s
  - `run.queued` → first `job.started` p95 ≤ 30 s (warm)
  - GitHub status freshness: ≤ 10 s behind platform state
  - Reporter success ≥ 99.9% (with retry)
- **Business dashboards** (from projections): DORA per team, build health, flake leaderboard, cost per team and pipeline kind, agent acceptance rate, scope ledger (ignored events).

## 4. Reliability patterns

| Failure | Response |
|---------|----------|
| Missed webhook | Reconciler diff (every 5 min for active repos, hourly for idle ones) plus GitHub "redeliver failed deliveries" sweep |
| Bus outage | Ingress keeps writing to the raw archive (outbox). Normalizer catches up from the archive. |
| GitHub API rate limits | Central token broker with per-installation budget, conditional requests (ETags), batching of annotations, backoff, and status coalescing (only the latest state per check run) |
| Executor node loss | Heartbeats → `ap.pipeline.job.lost` → reschedule (idempotent attempt++) |
| Orchestrator crash | Durable workflow replay (Temporal); no lost state |
| Poison event | DLQ per consumer, alert, fix, bulk replay |
| Bad mapping / handler bug | Replay from L0/L1 into a sandbox namespace, compare outputs, then replay into production with `origin=replay` |
| Agent provider outage | Fall back to rules-only diagnosis and L0 mode. Runs still complete. Coding sessions suspend and resume from their last checkpoint. Work items show `blocked{provider}` in Jira. |
| Jira outage / API limits | Inbound: the reconciler polls tracked items on recovery. Outbound: the Work Reporter queues and coalesces comments and transitions. |
| Dev sandbox loss | Session state = branch + checkpoint events. Re-provision and resume from the last pushed commit plus the session journal. |

## 5. Disaster recovery

- Raw archive: cross-region replicated object storage (RPO 0 for accepted webhooks).
- Event store: replicated, with point-in-time recovery. Projections can be rebuilt from events.
- Control plane: active/passive across regions. Executors are regional and stateless.

## 6. Data retention

| Data | Hot | Cold |
|------|-----|------|
| Raw webhooks (L0) | 90 d | 1 y |
| Canonical events (L1–L3) | 1 y | 3 y (audit) |
| Logs | 30 d | 1 y (compressed) |
| Test reports / SARIF | 90 d | — |
| Artifacts | per channel policy (PR snapshots 14 d, mainline 90 d, releases indefinite) | — |
| Agent traces | 30 d | 1 y (inputs by reference) |
