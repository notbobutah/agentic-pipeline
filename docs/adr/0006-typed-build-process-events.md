# ADR-0006: Typed build-process events emitted by an executor sidecar

**Status:** Proposed · **Date:** 2026-11-20

## Context
Treating a job as a black box (started/completed plus a log) forces failure diagnosis, performance analysis and supply-chain evidence to be reconstructed from unstructured logs, which is expensive (LLM tokens) and unreliable.

## Decision
Define a canonical, language-agnostic build lifecycle (docs/12) and emit typed events per phase from an `ap-sidecar` in each executor. The sidecar uses versioned tool-output parsers (Maven, Gradle, npm, pytest, go, cargo, dotnet, JUnit, SARIF, BuildKit, …). Events are tiered (bus / summary / telemetry). Handlers subscribe only to bus/summary events.

## Consequences
- Most failures are classified deterministically (no LLM). Precise annotations. Provenance from observed facts.
- Parser maintenance per ecosystem. Unknown tools fall back to generic step events plus an LLM Diagnoser.
- Bus volume increases by ~20–45 events per job. Within capacity.

## Alternatives
Logs only plus an LLM (cost, accuracy), or per-ecosystem event types (fragmented handlers).
