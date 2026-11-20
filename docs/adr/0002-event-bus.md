# ADR-0002: Kafka-compatible log as the event bus, partitioned by repo

**Status:** Proposed · **Date:** 2026-11-20

## Context
We need durable, replayable, per-repo ordered delivery, many independent consumer groups (hot/warm/cold), and retention long enough for re-processing.

## Decision
A Kafka-compatible log (Redpanda self-hosted or a managed Kafka). Partition key = `repo`. Topics are split by layer and domain (`ap.l1.change`, `ap.l3.pipeline`, `ap.l3.build`, …), with a consumer group per handler and DLQ topics per handler.

## Consequences
- Replay and new-consumer backfill for free; strict per-repo ordering.
- Hot-repo skew must be managed (sub-partitioning by `subject` for monorepos).
- Operational weight is higher than a cloud pub/sub.

## Alternatives
NATS JetStream (simpler ops, subject-based routing, weaker ecosystem for long replay), Pulsar (tiered storage, more complex), cloud pub/sub (no ordering or replay guarantees across all providers).
