# 07 — Agents

## 1. Role of agents

Agents do two kinds of work:

1. **Development work** (work plane, [14](14-work-plane.md)): planning how to implement a Jira task, writing tests, writing code, and iterating on feedback. This is the primary value of the system.
2. **Delivery support** (delivery plane): understanding an unfamiliar repo, writing a build plan, explaining a failure, fixing routine breakages on human PRs.

Everything else is deterministic code. Agents are invoked **only** through the Agent Gateway, and **only** when a deterministic handler or orchestrator decides it is worth it ([13 §1 rule 7](13-event-handlers.md#1-handler-rules)). Agents never push, comment, transition Jira or publish events themselves. They return proposals.

## 2. Agent roster

### 2.1 Work-plane agents

| Agent | Invoked by | Inputs | Output (proposal kind) | Notes |
|-------|-----------|--------|------------------------|-------|
| **Implementation Planner** | Work Item Orchestrator (H-WRK-03) | work item (untrusted), sponsor guidance (trusted), resolved repos, read-only checkouts, repo profiles, ADRs/docs, similar past work items and PRs | `implementation-plan` ([14 §5](14-work-plane.md#5-implementation-plan-what-the-developer-approves)) or `clarification` | Must map every acceptance criterion to at least one test. Must declare a scope envelope. **Its output always goes to the developer for approval.** |
| **Test Author** | Session Orchestrator (H-WRK-06) | approved plan `tests` section, acceptance criteria, the repo's existing test conventions | `commit` (tests only) | A separate instance from the Coding agent; never sees the implementation it is testing ([14 §6.2](14-work-plane.md#62-test-first-with-independent-test-authorship)) |
| **Coding agent** | Session Orchestrator | approved plan, tests from the Test Author, dev sandbox tools (edit, build, run tests using Build Plan commands), structured feedback | `commit`, `clarification`, `deviation-request` | Works within the scope envelope. Iterates in the inner loop before proposing a commit. Writes the PR description with AC → test evidence. |

### 2.2 Delivery-plane agents

| Agent | Invoked by | Inputs | Output (proposal kind) | Typical cadence |
|-------|-----------|--------|------------------------|-----------------|
| **Profiler** | H-LIF-02 | repo tree listing, build files, CI files, CODEOWNERS, lockfiles | `ap.repo.profile.updated` (via proposal `kind=manifest`) | on install; on build-file change; monthly |
| **Build Planner** | H-ORC-03 (cache miss) | profile, manifest, executor image catalog, org templates | `build-plan` (DAG) | per fingerprint change (rare). The Build Plan also gives coding sessions their inner-loop build and test commands. |
| **Diagnoser** | H-DIAG-02 | typed failure events, log excerpts (windowed around error), diff, recent history, flake stats | `ap.diagnosis.completed` | only when rules can't classify |
| **Remediator** | H-DIAG-05 | diagnosis, repo checkout (read-only sandbox), tool access (build/test in sandbox) | `patch` / `pr` / `quarantine` / `manifest` | per fixable failure on **human-authored** changes and mainline. Failures on session-owned changes go to the owning Coding agent instead. |
| **Reviewer** *(optional)* | Router on `review.requested{bot}` | diff, gates, diagnosis history | `comment` | per request |
| **Release** | H-REL-01 | commits since last release, change labels, conventional commits | `version-bump`, `release-notes` | per release |
| **Dependency** | schedule, `toolchain.deprecated`, `upstream.released` | SBOM, changelogs, breaking-change notes | `pr` (grouped upgrades) | weekly / on demand |
| **Security** | `vulnerability.alert.opened`, `code_scanning.alert.opened` | advisory, reachability, code context | `patch` / `pr` / `comment` (VEX justification) | per alert |

## 3. Build Plan

The Build Planner's output is a **declarative, validated DAG**, never a script that runs unchecked.

```yaml
planVersion: 1
fingerprint: sha256:…          # hash(manifest, build files, lockfiles, toolchain decls, executor image digests, org template version)
projects:
  - id: api
    path: services/api
    toolchain: { jdk: "21", gradle: wrapper }
jobs:
  - key: api:build
    phase: [toolchain, dependencies, compile]
    pool: linux-amd64-medium
    steps:
      - uses: ap/setup-jdk@v3        # vetted step library, not arbitrary shell, by default
      - run: ./gradlew :services:api:assemble --build-cache
    caches: [gradle-deps, gradle-build]
    outputs: [build/libs/*.jar]
  - key: api:test
    needs: [api:build]
    phase: [test]
    steps:
      - run: ./gradlew :services:api:test
    reports: [{ kind: junit, path: "**/build/test-results/**/*.xml" }]
  - key: api:image
    needs: [api:test]
    phase: [package]
    platforms: [linux/amd64, linux/arm64]
    steps:
      - uses: ap/oci-build@v2
        with: { context: services/api, dockerfile: Dockerfile }
gates: [tests, sast, vuln-threshold, provenance]
```

**Validation** (deterministic, before use): schema validity, allowed pools, allowed `uses:` steps, no secrets referenced in untrusted contexts, no network access outside the egress profile, phase ordering consistent with [12 §2](12-build-process-events.md#2-the-canonical-build-lifecycle), and a **dry-run** on the default branch before the plan is marked `trusted`.

**Plan precedence:** a committed manifest (`.agentic/pipeline.yaml`, which humans own) overrides a cached generated plan, which overrides fresh generation. Generated plans are offered back as a PR (`ap.build.plan.committed`), so teams can review them and take ownership.

## 4. Control model

### 4.1 Work-plane controls (fixed, not configurable below these floors)

| Control | Rule |
|---------|------|
| Plan approval | **Required** from the sponsoring developer or an owning-team delegate before any session starts ([ADR-0007](adr/0007-developer-approved-implementation-plan.md)) |
| Scope envelope | Commits outside the approved repos or paths, touching forbidden paths, or weakening Test Author tests ⇒ `ap.work.plan.deviation.detected` ⇒ re-approval |
| Branch | Commits only to agent-owned branches `ap/<KEY>/<repo>`, via the Applier (auto-applied by policy because the branch is agent-owned and the PR still needs review) |
| Merge | **Always human.** Agent-authored PRs require ≥ 1 human approval. L4 auto-merge never applies to work-plane changes. |
| Budgets | Per session and per work item (iterations, cost, wall-clock) with escalation ([14 §6.5](14-work-plane.md#65-stopping-conditions)) |

### 4.2 Autonomy levels (delivery-plane remediation on human changes)

Set per repo (default from team or org), and enforced by the Proposal Gatekeeper (H-DIAG-06) via policy.

| Level | Name | Agents may… |
|-------|------|-------------|
| L0 | Observe | Diagnose and comment only |
| L1 | Suggest | Plus propose fixes as **suggestions** (PR comments / check-run actions a human clicks) |
| L2 | Branch | Plus open **separate PRs** with fixes (humans review and merge) |
| L3 | Amend | Plus push commits **to the change's own branch** for low-risk classes (format, lockfile, codegen, flake quarantine) |
| L4 | Autonomous | Plus auto-merge agent PRs that pass all gates for allow-listed classes (e.g. patch-level dependency bumps) |

Risk scoring caps the effective level per proposal. A diff touching `**/auth/**`, CI config or more than N lines drops it to L1, regardless of repo level.

## 5. Agent Gateway responsibilities

- **Budgeting.** Per task, repo, team and tenant (tokens, USD, wall-clock). Emits `ap.agent.budget.exceeded`.
- **Model routing.** A cheap, fast model for classification and summarisation. The most capable model for planning and remediation. Recorded on each task.
- **Tooling.** Agents get a scoped tool set: read repo (sandboxed checkout), run a command in an **ephemeral sandbox with no secrets**, read logs and reports by reference, search past diagnoses. No direct GitHub write and no bus publish. Outputs are proposals only.
- **Context hygiene.** Repo content, logs, PR text and comments are wrapped as **untrusted data**. Instructions in that content are never followed. A guardrail model scans outputs for injection artefacts, secrets and forbidden-path edits (`ap.agent.guardrail.triggered`).
- **Determinism aids.** Prompt and tool versions are pinned and recorded. Temperature is low for classification. Outputs must match a JSON schema per proposal kind.
- **Audit.** Each task stores inputs (by reference), output, model, prompt version, token counts, cost and an OTel trace.

## 6. Cost control

| Lever | Effect |
|-------|--------|
| Plan approval before coding | No coding spend on a misunderstood task: the cheapest point to correct direction |
| Inner loop before outer loop | Most compile and test errors are fixed in the sandbox, not through full pipeline runs |
| Structured feedback (typed events + log windows) | Coding agent iterations use small, precise context |
| No-progress detection (same failure signature twice) | Stops expensive loops early, escalates with a summary |
| Plan cache by fingerprint | > 95% of runs need no Planner call |
| Deterministic Failure Classifier first | Most failures classified with no LLM (typed build events) |
| Diagnosis dedupe | Same `(error signature, repo)` within 24 h reuses the prior diagnosis |
| Log windowing | Send ±200 lines around the typed error location, not the full log |
| Tiered models | Small model triages; large model only on escalation |
| Budgets and quotas | Hard caps per tenant, graceful degradation to L0 (comment-only) |

## 7. Evaluation

- Golden datasets of historical failures (from `ap.diagnosis.feedback.received` and applied/discarded proposals).
- Offline replay: re-run agents against archived events in a sandbox namespace when prompts or models change, and compare against labelled outcomes before rollout.
- Online metrics from H-PRJ-08: proposal acceptance rate, revert rate of applied fixes, diagnosis precision per class.
