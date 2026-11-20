# 05 — Source Mappings

> How raw provider events (L0) become canonical events (L1). Mappings are versioned code (`mappingVersion`) in the Normalizer. Re-normalising from the raw archive with a new mapping version is supported.

## 1. GitHub App: subscribed webhooks

The App subscribes **only** to the events below. Anything else is unsubscribed at the App level, which keeps ingress volume and scope bounded.

| GitHub event | `action` / condition | Canonical event(s) | Notes |
|--------------|----------------------|--------------------|-------|
| `installation` | `created` | `ap.tenant.installed` + `ap.repo.added` × n | Triggers the Reconciler's initial sync |
| | `deleted` | `ap.tenant.uninstalled` | Cancel everything, GC caches |
| | `suspend` / `unsuspend` | `ap.tenant.suspended` / `…unsuspended` | |
| | `new_permissions_accepted` | `ap.tenant.permissions.changed` | |
| `installation_repositories` | `added` / `removed` | `ap.repo.added` / `ap.repo.removed{deselected}` | |
| `repository` | `created` | `ap.repo.added` (if selection = all) | |
| | `deleted` | `ap.repo.removed{deleted}` | |
| | `renamed` | `ap.repo.renamed` | Repo ID is stable; names are not |
| | `transferred` | `ap.repo.transferred` | May cross tenants |
| | `archived` / `unarchived` | `ap.repo.archived` / `…unarchived` | |
| | `publicized` / `privatized` | `ap.repo.visibility.changed` | Policy-relevant (caches, secrets) |
| | `edited` (default_branch) | `ap.repo.default_branch.changed` | Other edits are ignored |
| `push` | branch, not deleted | `ap.scm.ref.updated` | `forced`, `isDefaultBranch` derived. **Ignored** if `after` = `0000…` (deletion is a separate mapping). |
| | `deleted: true` | `ap.scm.ref.deleted` | |
| | tag ref, created | `ap.scm.tag.created` | Also `ap.scm.ref.created{tag}` |
| | changes `.agentic/**` | + `ap.repo.manifest.changed` (emitted by Orchestrator after validation) | |
| `create` / `delete` | — | *(ignored, duplicates of `push`)* | Recorded in `ap.raw.ignored{duplicate-of-push}` |
| `pull_request` | `opened` | `ap.change.opened` | |
| | `synchronize` | `ap.change.updated` | Most frequent event. Drives supersession. |
| | `reopened` | `ap.change.reopened` | |
| | `edited` with `changes.base` | `ap.change.retargeted` | |
| | `edited` (title/body) | `ap.change.edited` | No rebuild |
| | `ready_for_review` / `converted_to_draft` | `ap.change.ready_for_review` / `…converted_to_draft` | |
| | `labeled` / `unlabeled` | `ap.change.labeled` / `…unlabeled` | |
| | `review_requested` | `ap.change.review.requested` | |
| | `closed` + `merged=true` | `ap.change.merged` | |
| | `closed` + `merged=false` | `ap.change.closed` | |
| | `assigned`, `locked`, `milestoned`, `auto_merge_*`, `enqueued` / `dequeued` | *(ignored v1)* | `enqueued`/`dequeued` are covered by `merge_group` |
| `pull_request_review` | `submitted` | `ap.change.review.submitted` | |
| | `dismissed` | `ap.change.review.dismissed` | |
| `pull_request_review_comment` | `created` | `ap.change.comment.created{isReviewComment}` | Feeds the command parser (e.g. `/ap fix` on a line) |
| `issue_comment` | `created` on a PR | `ap.change.comment.created` | Issue (non-PR) comments are ignored v1. On agent-owned PRs, sponsor/CODEOWNER comments become session guidance via the Feedback Router. |
| `merge_group` | `checks_requested` | `ap.mergequeue.entry.checks_requested` | Must report a required check on the `merge_group` head SHA |
| | `destroyed` | `ap.mergequeue.entry.destroyed` | |
| `check_suite` | `rerequested` (our app) | `ap.command.received{retry, channel=check-rerequest}` | The "Re-run all" button |
| | `completed` (other apps) | `ap.extci.run.completed` | Coexistence mode |
| `check_run` | `rerequested` (our app) | `ap.command.received{retry-failed}` | |
| | `requested_action` (our app) | `ap.command.received{<action identifier>}` | Check-run buttons: "Apply fix", "Quarantine test" |
| | `completed` (other apps) | `ap.extci.check.completed` | |
| `workflow_run` | `completed` | `ap.extci.run.completed{system=github-actions}` | Observe GitHub Actions during migration |
| `status` | (other systems) | `ap.extci.check.completed` | Legacy commit statuses |
| `release` | `published` | `ap.release.published` (if not created by us) or `ap.release.requested{manual}` | Configurable per repo |
| `deployment_protection_rule` | `requested` | `ap.approval.requested` → answered via API | Lets agentic-pipeline gate GitHub Environments |
| `deployment_status` | (external CD) | `ap.deployment.completed` / `…started` | When CD is external |
| `dependabot_alert` | `created` / `reopened` | `ap.security.vulnerability.alert.opened` | |
| | `fixed` / `dismissed` / `auto_dismissed` | `ap.security.vulnerability.alert.resolved` | |
| `code_scanning_alert` | `created` / `reopened` / `appeared_in_branch` | `ap.security.code_scanning.alert.opened` | |
| | `fixed` / `closed_by_user` | `ap.security.code_scanning.alert.resolved` | |
| `secret_scanning_alert` | `created` | `ap.security.secret.detected` | P0 path |
| `registry_package` / `package` | `published` | `ap.dependency.upstream.released{isInternal=true}` | Internal fan-out |
| `repository_dispatch` | allow-listed `event_type`s | `ap.command.received{channel=api}` | Escape hatch for external systems |
| `workflow_dispatch` | — | *(not subscribed)* | Not our workflow system |
| `ping` | — | health only | |

## 2. Normalizer filters (pre-mapping)

| Filter | Rule | Ledger reason |
|--------|------|---------------|
| Bot loop | Ignore `push`/`synchronize` authored by the agentic-pipeline bot **unless** the commit is an applied proposal (trailer `Ap-Proposal-Id:`). That case maps normally with `actor.kind=agent`. Coding-session commits also carry `Ap-Session-Id:` and `Ap-Work-Item:`, which the Normalizer copies into `data.agentAuthored{sessionId, workItem}` and the correlation `links`. | `bot-loop` |
| Archived repo | All change/scm events on archived repos | `archived-repo` |
| Unregistered repo | Repo not in the Registry (race on install). Buffer for 60 s, then reconcile. | `unregistered` |
| Path-only ignore | Repo manifest `ignorePaths` (e.g. `docs/**`). Event is still emitted, with `data.allPathsIgnored=true`. The Router decides. | — |
| Skip directive | `[skip ap]` / `[ap skip]` in the head commit message. Emitted, with `data.skipRequested=true`. | — |
| Oversized payload | `push` with > 2,048 commits or a truncated `commits[]`. Emitted with `truncated=true`, and the Orchestrator computes the diff via API. | — |

> Filters prefer **emit and annotate** over **drop**, so routing decisions stay in one place (the Trigger Router) and are visible in the timeline.

## 3. Jira (work tracker)

Delivered via a Jira Cloud app (Forge/Connect: JWT-verified) or admin webhooks (shared-secret header). Write-back uses OAuth 2.0 as the app's own actor. Only **configured projects** are subscribed.

| Jira webhook | Condition | Canonical event(s) | Notes |
|--------------|-----------|--------------------|-------|
| `jira:issue_updated` | changelog `assignee` → agent user | `ap.work.item.assigned{toAgent=true}` | Primary trigger |
| | changelog `assignee` from agent user → other | `ap.work.item.unassigned` | Cancels active work |
| | changelog `assignee` (not involving agent) | *(ignored)* | Ledger `not-agent-assignment` |
| | changelog `status` | `ap.work.item.transitioned` | Status triggers are opt-in per project |
| | changelog `description` / AC custom field / `summary` / `components` | `ap.work.item.updated{fields}` | May supersede an approved plan |
| | changelog `labels` contains `ap-agent` added | `ap.work.item.assigned{trigger=label}` | Opt-in |
| | other fields (story points, sprint, priority…) | *(ignored)* | |
| `jira:issue_created` | assignee = agent user at creation | `ap.work.item.assigned` | |
| `jira:issue_deleted` | tracked item | `ap.work.item.deleted` | |
| `comment_created` | on a tracked item | `ap.work.item.commented` → Work Comment Parser → `ap.work.guidance.provided` / `ap.work.clarification.answered` / `ap.command.received` (`/ap approve`, `/ap revise`, `/ap cancel`, `/ap sponsor`, `/ap start`) | Agent's own comments ignored (`bot-loop`) |
| `comment_updated` | edits to a guidance comment before plan approval | `ap.work.item.commented{edited=true}` | After approval, edits are ignored (approval is bound to a plan digest) |
| `issuelink_created` / `_deleted` | tracked item | `ap.work.item.linked` | `blocks` links can hold a work item |
| sprint, board, worklog, version, user events | — | *(not subscribed)* | |

**Identity mapping.** Jira account ID ↔ GitHub login ↔ team. It is resolved by the Registry from SSO/SCIM (preferred) or an explicit mapping table. Unmapped users cannot sponsor, guide or approve (`ap.work.request.rejected{no-identity-mapping}`).

**Other trackers (later):** Linear (`Issue` webhooks), GitHub Issues (`issues`, `issue_comment`). Both map onto the same `ap.work.*` types with `provider.name` set.

## 4. Other sources

| Source | Transport | Canonical event(s) |
|--------|-----------|--------------------|
| Platform scheduler | internal | `ap.schedule.fired` |
| Console / REST / gRPC API | authenticated API | `ap.command.received{channel=api|console}` |
| Slack/Teams ChatOps | app interactions | `ap.command.received{channel=chat}` |
| npm / PyPI / Maven Central / crates.io / Go proxy feeds | polling or webhook adapters | `ap.dependency.upstream.released{isInternal=false}` (only for packages present in the SBOM index) |
| OSV / GHSA feeds | polling | `ap.security.advisory.published` |
| Argo CD / Flux / Spinnaker | notifications / webhooks | `ap.deployment.started/completed`, `ap.deployment.rolled_back` |
| Prometheus / Datadog / SLO burn alerts | Alertmanager / webhook | `ap.environment.health.degraded/recovered` |
| PagerDuty / Opsgenie | webhook | `ap.environment.incident.opened/closed` |
| Renovate / Dependabot PRs | already arrive as `pull_request` | `ap.change.*` plus `ap.dependency.update.proposed` (detected by author/labels) |
| GitLab (future) | webhook | same `ap.change.*`, `ap.scm.*` with `provider.name=gitlab` |

## 5. Mapping guarantees

- **Deterministic.** The same raw input with the same mapping version gives byte-identical canonical events (IDs derived as `uuidv5(deliveryId, index)`).
- **Total.** Every raw event yields either ≥ 1 canonical event or exactly one `ap.raw.ignored`.
- **Versioned.** Changing a mapping bumps `mappingVersion`. Replays carry `origin=replay` and do not re-trigger side effects unless replayed into a sandbox namespace.
