# ADR-0005: Agents emit proposals; deterministic policy applies them

**Status:** Proposed · **Date:** 2026-11-20

## Context
LLM agents are non-deterministic, exposed to untrusted input (prompt injection), and can be wrong with high confidence. Yet their value comes from acting (fixing, planning, releasing).

## Decision
Agents never call GitHub, publish events or mutate state. They return typed proposals (`ap.agent.proposal.created`). The Proposal Gatekeeper evaluates each one with OPA policy (autonomy level, risk score, touched paths, confidence) and then auto-applies it, requests approval, or discards it. Application is done by a deterministic Applier with idempotency on `proposalId`.

## Consequences
- Every agent effect is auditable and reversible by policy.
- Some latency (an extra hop) and policy-authoring effort.
- Clean evaluation data: proposals plus outcomes form a labelled dataset.

## Alternatives
Agents with direct tool access to GitHub (rejected: blast radius, injection), or human approval for everything (rejected: does not meet the automation goals).
