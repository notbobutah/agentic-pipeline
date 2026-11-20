# ADR-0004: GitHub App as the sole SCM integration

**Status:** Proposed · **Date:** 2026-11-20

## Context
We need org-wide webhooks, fine-grained permissions, the Checks API (annotations, actions), merge queue support, and no dependence on individual user tokens.

## Decision
A single GitHub App per deployment, installed per org. It subscribes only to the events in docs/05. Installation tokens are minted per operation by a central token broker. Write-scoped operations go only through the Reporter and the Applier.

## Consequences
Checks API status and buttons (`requested_action`), bot identity for commits, rate limits per installation (must be budgeted), and no PATs to rotate.

## Alternatives
OAuth app or PATs (user-bound, coarse scopes), org webhooks plus a separate bot account (no Checks API).
