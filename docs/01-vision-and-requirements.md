# 01 — Vision & Requirements

## 1. Problem

Organisations with dozens of teams across many repos, languages and architectures lose engineering time in two places:

1. **Turning well-understood tasks into code.** Many tickets are clear: "add idempotency keys to refunds", "expose field X in API Y", "migrate client Z to the v2 SDK". They still take days of developer time to implement, test, get through CI and review.
2. **Getting code through delivery.** Every team has its own CI dialect. Quality gates are inconsistent, and flaky tests, broken dependency bumps and red main branches all need a human to read logs. There is no organisation-wide view of a change's journey.

## 2. Vision

> A developer assigns a Jira task to the agent and adds some guidance. The agent proposes an implementation plan, and the developer approves it. Agents then work in the background, writing the code **and** the tests and committing them to the repos involved. An event-driven pipeline builds, tests and integration-tests every commit, and routes failures back to the agents until the change is green. The developer reviews a finished, evidenced pull request and merges it.

The system has two cooperating planes on one event backbone:

| Plane | Responsibility | Docs |
|-------|----------------|------|
| **Work plane** | Work item intake, guidance, implementation planning, **developer plan approval**, coding sessions, feedback loop, multi-repo changesets, Jira write-back | [14](14-work-plane.md) |
| **Delivery plane** | Event ingestion, build/test/scan pipelines, integration testing in ephemeral environments, gates, supply chain, release and deploy, failure diagnosis | [02](02-architecture-overview.md)–[06](06-pipeline-lifecycle.md), [12](12-build-process-events.md), [13](13-event-handlers.md), [15](15-integration-testing.md) |

The delivery plane also serves **human-authored** changes, using the same pipeline, gates and diagnosis. Agent-authored changes get no special path through verification. They get *more* verification (mandatory integration testing).

## 3. Goals

| ID | Goal |
|----|------|
| G1 | **Task to merged change.** An eligible Jira task assigned to the agent becomes a reviewed, tested, merged change with only three human touchpoints: guidance, plan approval and PR review. |
| G2 | **Developer stays in control.** No code is written before the developer approves a plan. No agent-authored code merges without human review. Scope beyond the approved plan needs re-approval. |
| G3 | **Tests are part of the deliverable.** Every agent change includes unit and integration tests mapped to the acceptance criteria, written test-first by an independent agent instance. |
| G4 | **Event-driven end to end.** Every stimulus (Jira, GitHub, schedules, commands) and every state transition is a canonical event. The event catalog is the definition of scope. |
| G5 | **Integration-tested by default.** Agent changes are verified in ephemeral environments against real neighbours, including all repos changed for the same task. |
| G6 | **Polyglot, zero-config delivery.** Any mainstream language or build system works without hand-written pipeline config. The Profiler and Build Plan also tell the coding agents how to build and test each repo. |
| G7 | **Closed feedback loop.** Typed pipeline results (not raw logs) flow back to the coding session that owns the change, which iterates until green or escalates with a clear summary. |
| G8 | **Secure supply chain by default.** SLSA L3 provenance, signed artifacts, SBOMs, no long-lived secrets, untrusted-input handling for all agent context. |
| G9 | **Full traceability.** One correlation runs from Jira task → plan → sessions → commits → runs → integration → merge → deploy, with cost and lead time per ticket. |

## 4. Non-goals (v1)

- Replacing Jira or GitHub. They stay the systems of record for work and code.
- Autonomous merging of agent-authored code. Merge is always a human decision.
- Agents choosing *what* to work on. Work enters only through explicit assignment by a human.
- Large, ambiguous work (epics, greenfield architecture). Intake rejects these or asks for them to be broken down.
- Production runtime orchestration. We emit deployment intents; Argo CD, Flux or similar executes them.
- SCMs and trackers beyond GitHub and Jira on day one (the model is provider-neutral; Linear, GitHub Issues and GitLab come later).

## 5. Functional requirements

### 5.1 Work intake & guidance
- FR-W1 Receive Jira webhooks (issue assigned / transitioned / updated / commented). Trigger work when a task is assigned to the agent user, or by configured status, label or command.
- FR-W2 Check eligibility (issue type, identity mapping, repo resolution, quotas, policy) and record accept/reject with reasons.
- FR-W3 Accept developer guidance from Jira comments, the console, the CLI and Slack. Only the sponsoring developer and owning-team members are treated as instruction sources.
- FR-W4 Ask clarifying questions when acceptance criteria are missing or ambiguous, and pause until they are answered or expire.
- FR-W5 Resolve the repos affected by a task (component mapping, service catalog, agent inference).

### 5.2 Implementation plan & approval
- FR-P1 Produce a structured, versioned implementation plan: approach, repos, files, tests mapped to acceptance criteria, risks, out-of-scope items, estimates and a scope envelope.
- FR-P2 **Require the sponsoring developer's (or a delegate's) approval of a specific plan version before any coding begins.**
- FR-P3 Support revision cycles (`/ap revise`) with full version history.
- FR-P4 Invalidate the approved plan when the task's description or acceptance criteria change materially, and when a session needs to go beyond the scope envelope.

### 5.3 Coding sessions
- FR-C1 Run one coding session per repo in an isolated dev sandbox with no production secrets.
- FR-C2 Write tests first (a separate Test Author agent instance), then implement until tests pass in the inner loop.
- FR-C3 Commit through the deterministic Applier to agent-owned branches, open draft PRs, and follow commit and PR conventions (trailers, plan link, AC → test evidence).
- FR-C4 Consume structured pipeline, integration and review feedback, and iterate within budgets.
- FR-C5 Stop and escalate on: no progress, budget exceeded, plan deviation, clarification needed, guardrail trigger.
- FR-C6 Mark PRs ready for review only when all gates and integration tests pass, and request the sponsor and CODEOWNERS as reviewers.
- FR-C7 Coordinate multi-repo changesets: joint integration, dependency-ordered merges, partial-merge handling.

### 5.4 Delivery: ingestion & event model
- FR-1 Receive GitHub App webhooks for all subscribed event types ([05](05-source-mappings.md)), verify signatures, deduplicate, and archive raw payloads.
- FR-2 Reconcile missed webhooks by diffing GitHub and Jira state against projections.
- FR-3 Normalise all inputs into a canonical CloudEvents 1.0 envelope with tenant, team, repo, correlation and causation. Use a versioned schema registry.
- FR-4 Support replaying any event stream into a sandbox namespace.

### 5.5 Delivery: pipeline
- FR-5 Route canonical events to pipeline intents via a declarative scope matrix plus per-repo policy, with concurrency groups and supersession.
- FR-6 Produce and cache a declarative Build Plan per repo fingerprint. Execute jobs in ephemeral, isolated sandboxes with remote caching.
- FR-7 Emit typed build-process events per phase ([12](12-build-process-events.md)).
- FR-8 **Run integration tests in ephemeral environments** for agent-authored changes (and opted-in human changes), with failure attribution across services ([15](15-integration-testing.md)).
- FR-9 Report to GitHub through the Checks API. Support merge queues.
- FR-10 Produce signed artifacts, SLSA provenance and SBOMs. Support release and deployment flows.

### 5.6 Diagnosis & remediation
- FR-11 Classify every failed job deterministically where possible, and with the Diagnoser agent otherwise.
- FR-12 Route feedback to the owning coding session for agent changes. Propose remediations for human changes as autonomy allows.

### 5.7 Human interface
- FR-13 Commands in Jira comments and PR comments (`/ap approve`, `/ap revise`, `/ap retry`, `/ap explain`, `/ap cancel`, …) with permission checks.
- FR-14 Write status back to Jira at each lifecycle step.
- FR-15 A console with a per-work-item timeline (plan versions, sessions, iterations, runs, environments, cost).
- FR-16 Notifications routed by sponsor and team ownership.

## 6. Non-functional requirements

| ID | Category | Requirement (initial target) |
|----|----------|------------------------------|
| NFR-1 | Scale | 100+ teams, 2,000+ repos. 500 webhook deliveries/s peak. 50k jobs/day. **200 concurrent coding sessions. 100 concurrent ephemeral environments.** |
| NFR-2 | Latency | Webhook → `run.queued` ≤ 5 s p95. Task assigned → plan proposed ≤ 10 min p90. Plan approved → first push ≤ 30 min p90. |
| NFR-3 | Durability | No accepted webhook is ever lost. Work items and sessions survive any component restart. |
| NFR-4 | Availability | Ingress 99.95%. Delivery plane 99.9%. Work plane 99.5% (sessions resume after outages). |
| NFR-5 | Isolation | Tenants cannot read each other's code, secrets, caches, environments or logs. Dev sandboxes and integration environments never hold production credentials. |
| NFR-6 | Fairness | Weighted fair share for jobs, sessions and environments. One team's burst cannot starve others. |
| NFR-7 | Cost | Budgets per session, work item, team and tenant. Plan-cache hits mean no Build Planner LLM call on ordinary pushes. Cost per merged ticket is reported. |
| NFR-8 | Auditability | Every plan version, approval, agent proposal, policy decision and commit is attributable. Retained ≥ 1 year. |
| NFR-9 | Evolvability | Schemas evolve without lock-step deploys. |
| NFR-10 | Explainability | For any merged agent change: which ticket, which guidance, which approved plan, which tests, which runs, who reviewed — from events alone. |

## 7. Success metrics

| Metric | Target (v1) |
|--------|-------------|
| Plan approved on the first or second version | > 75% |
| Eligible tasks reaching a green, ready-for-review PR without escalation | > 60% |
| Agent PRs merged with ≤ 1 review round | > 50% |
| Agent PRs reverted or hot-fixed within 14 days | < 3% |
| Outer-loop iterations to green (p50) | ≤ 3 |
| Failed jobs classified correctly (rules + Diagnoser) | > 85% |
| Time to first green build for a newly installed repo, no config | < 15 min |
| Developer time per agent ticket (guidance + approval + review) | < 25% of manual implementation time |
