# ADR-0003: Durable workflows (Temporal) for run orchestration

**Status:** Proposed · **Date:** 2026-11-20

## Context
Runs are long-lived (minutes to days when awaiting approval), involve timers, retries, human waits, cancellation and compensation, and must survive restarts. Hand-rolled event-sourced sagas are error-prone.

## Decision
Each run, release and deployment is a Temporal workflow keyed by its aggregate ID. Workflows consume bus events via signals (a bridge consumer) and emit L3 facts via activities that use a transactional outbox. The bus remains the integration backbone; Temporal is internal to the Orchestrator.

## Consequences
- Clear state machines in code, built-in timers and retries, and full history for debugging.
- Two sources of history (workflow history and the bus). The bus is canonical for integration and audit.
- Workflow code must be deterministic (Temporal constraints).

## Alternatives
Argo Workflows (Kubernetes-native, but weaker for long human waits and event signals), AWS Step Functions (vendor lock-in), choreography only (hard to reason about at this scale).
