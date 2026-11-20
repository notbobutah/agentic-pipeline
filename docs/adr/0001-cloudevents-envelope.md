# ADR-0001: CloudEvents 1.0 envelope with platform extensions

**Status:** Proposed · **Date:** 2026-11-20

## Context
Events come from many producers (ingress, orchestrator, executors, agents, external adapters) and are consumed by many handlers. We need a uniform envelope for routing, tenancy, tracing and dedupe that does not depend on payload parsing.

## Decision
Use CloudEvents 1.0 (structured JSON mode) with the extension attributes `tenant`, `team`, `repo`, `partitionkey`, `correlationid`, `causationid`, `traceparent`, `origin`, `actor`, `sensitivity`, `sequence`, `providerdelivery`. The major version goes in `type` (`…v1`). Minor and patch versions go in `dataschema`.

## Consequences
- Off-the-shelf SDKs, and bindings for Kafka, HTTP and NATS.
- Routing, ACLs and quotas work on headers alone.
- Some verbosity per event (~600 bytes envelope). Acceptable.

## Alternatives
A custom envelope (no ecosystem), or Avro with a schema registry (stronger typing, but harder for polyglot producers and humans to debug). Revisit for high-volume streams.
