# agentic-pipeline — Agentic Development & Event-Driven Build Pipeline

> Status: **Design draft + working demo (phases P1–P4)** · Last updated: 2026-11-24 · What is built vs designed: [16 — Implementation Status](16-implementation-status.md)

agentic-pipeline takes a **Jira task assigned to an agent**, together with **guidance from a developer**. It proposes an implementation plan that **the developer approves**, then agents work in the background to **write code and tests** and commit them to the repos involved. An **event-driven build pipeline** builds, tests and **integration-tests** every commit, and feeds failures back to the agents until the change is green and ready for human review.

It serves **many teams**, across **many repositories**, in **many languages and architectures**.

The **event model is the spine of the system**. Every external stimulus (a Jira assignment, a push, a review, a CVE) and every internal transition (plan approved, job failed, integration test attributed to a repo) is a canonical event. **If something is not in the event catalog, it is out of scope.**

## Two planes, one event backbone

```
 WORK PLANE  (doc 14)                                DELIVERY PLANE  (docs 02–06, 12, 13, 15)
 Jira task + guidance                                 ingest → route → build → test → scan
   → implementation plan → DEVELOPER APPROVES           → integration (ephemeral env) → gates
   → coding sessions (tests first, then code)  ──push──►  → report to GitHub / Jira
   → iterate on structured feedback  ◄──results/diagnosis──┘
   → PR ready for review → human review → merge → Jira Done
```

## Reading order

| # | Document | Purpose |
|---|----------|---------|
| 01 | [Vision & Requirements](01-vision-and-requirements.md) | Goals, non-goals, functional and non-functional requirements |
| 02 | [Architecture Overview](02-architecture-overview.md) | Planes, components, end-to-end flows |
| 14 | [Work Plane](14-work-plane.md) | **Core.** Task intake, guidance, plan approval, coding sessions, feedback routing, Jira write-back |
| 15 | [Integration Testing](15-integration-testing.md) | **Core.** Ephemeral environments, failure attribution, multi-repo changesets |
| 03 | [Event Model](03-event-model.md) | **Core.** Envelope, layers, naming, versioning, ordering, correlation |
| 04 | [Event Catalog](04-event-catalog.md) | **Core.** Every event type (work plane + delivery plane) |
| 05 | [Source Mappings](05-source-mappings.md) | GitHub and Jira raw events → canonical events |
| 06 | [Pipeline Lifecycle](06-pipeline-lifecycle.md) | Run state machine, pipeline kinds, scope matrix (trigger → pipeline) |
| 07 | [Agents](07-agents.md) | Agent roster, contracts, autonomy, cost control |
| 08 | [Repository Contract](08-repository-contract.md) | Onboarding, profiling, the `.agentic/pipeline.yaml` manifest |
| 09 | [Security & Trust](09-security-and-trust.md) | Trust boundaries, supply chain, prompt-injection defence |
| 10 | [Scale, Tenancy & Operations](10-scale-tenancy-operations.md) | Multi-tenancy, fairness, observability, DR |
| 11 | [Open Questions](11-open-questions.md) | Decisions still pending |
| 12 | [Build Process Events](12-build-process-events.md) | The canonical build lifecycle and 95 typed events emitted inside a job (categories A–Q) |
| 13 | [Event Handlers](13-event-handlers.md) | Handler registry: categories, subscriptions, idempotency, failure policy |
| 16 | [Implementation Status](16-implementation-status.md) | What the code implements, the DEMO-1 run, known issues, demo stand-ins vs the design |
| — | [demo/](demo/README.md) | **Demo:** two GitHub repos + one card, end to end. Plan, runbook and status. |
| — | [catalog/](catalog/) | Machine-readable `build-events.yaml`, `work-events.yaml`, `handlers.yaml` |
| — | [Glossary](glossary.md) | Terms |
| — | [ADRs](adr/) | Architecture Decision Records |
| — | [Schemas](schemas/) | JSON Schemas and example events |

## The one-paragraph design

A Jira assignment arrives as a webhook. It is verified, archived and normalised into `ap.work.item.assigned.v1`. The **Work Intake Router** checks eligibility and resolves the affected repos. The **Implementation Planner** agent then produces a versioned plan (approach, files, tests mapped to acceptance criteria, scope envelope), and **the sponsoring developer must approve it**. Each approved repo gets a **coding session** in an isolated dev sandbox. A **Test Author** agent writes tests first from the acceptance criteria, then a **Coding agent** implements until they pass locally. Commits are applied through a deterministic Applier to agent-owned branches as draft PRs. Every push enters the **delivery plane** as `ap.change.*`. There, the Trigger Router starts `change-validation` and then `integration` runs, a durable orchestrator executes the cached Build Plan in ephemeral executors that emit **typed build-process events**, and integration suites run in **ephemeral environments** with failures attributed to the responsible repo. The **Feedback Router** sends those structured results (and human review comments) back to the owning session, which iterates until everything is green. The PR is then marked ready for review, a human reviews and merges it, and Jira is updated. Agents never act directly: they emit **proposals**, and policy plus the approved plan's scope decide what is applied.
