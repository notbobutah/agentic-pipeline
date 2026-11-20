# ADR-0009: Ephemeral environments for integration testing of agent changes

**Status:** Proposed · **Date:** 2026-11-20

## Context
Agent-written code can pass unit tests (even independently authored ones) while failing against real dependencies, real data and other services. Multi-repo tasks also need their changes verified together before any of them merges.

## Decision
- Add `integration` and `changeset-integration` pipeline kinds that deploy the change's PR artifacts into an **ephemeral environment** and run integration, contract and e2e-smoke suites.
- The default strategy is **baseline + overlay** (only changed services deployed, with header-based routing in a shared baseline). Full-stack, compose-in-job and simulated-neighbour strategies are available per system.
- Test data comes from **database branching** (preferred), seed fixtures or masked snapshots. Never production data or credentials.
- A **Failure Attributor** (with optional control runs against baseline) decides which repo or service caused a failure, so feedback only reaches the responsible session.
- Integration is **mandatory for agent-authored changes** in repos that declare `integration:`. An agent PR is not marked ready for review until it passes.

## Consequences
- Much higher confidence in agent changes, and migrations get tested for free on DB branches.
- Infrastructure cost and complexity (service mesh routing, environment lifecycle, TTL reaping). Mitigated by the overlay strategy and per-team caps.
- Baseline health becomes a dependency. A broken baseline must be detected (control runs) and must not be blamed on changes.

## Alternatives
In-job integration only (testcontainers, no real neighbours), shared staging (contention, poor isolation, no per-change attribution), or full stack per PR everywhere (too costly at this scale).
