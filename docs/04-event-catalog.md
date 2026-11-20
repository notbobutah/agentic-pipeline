# 04 — Event Catalog

> The authoritative list of canonical event types. **If a type is not listed here (and in the schema registry), it does not exist.** All types carry the envelope from [03 §4](03-event-model.md#4-envelope). Only the key `data` fields are shown here. Full schemas live in the registry.

Legend: **Class** F = fact, I = intent, S = signal. **Layer** L0–L3 per [03 §2](03-event-model.md#2-event-layers).

Machine-readable: [`catalog/work-events.yaml`](catalog/work-events.yaml) (work plane + integration) and [`catalog/build-events.yaml`](catalog/build-events.yaml) (build process, [12](12-build-process-events.md)).

---

## Work-plane domains

See [14 — Work Plane](14-work-plane.md). Handlers are `H-WRK-*` in [13](13-event-handlers.md#314-work-plane-h-wrk).

### work.item — L1 (from Jira / tracker)

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.work.item.assigned.v1` | F | Normalizer (Jira) | Work Intake Router | `key` (ACME-1234), `project`, `issueType`, `assignee`, `assignedBy`, `toAgent` (bool), `summary`, `priority`, `components[]`, `labels[]`, `provider{name,id,url}` |
| `ap.work.item.unassigned.v1` | F | Normalizer | Work Item Orchestrator (cancel) | `key`, `previousAssignee` |
| `ap.work.item.transitioned.v1` | F | Normalizer | Work Intake Router, Orchestrator | `key`, `fromStatus`, `toStatus`, `by` |
| `ap.work.item.updated.v1` | F | Normalizer | Work Item Orchestrator (plan supersede check) | `key`, `fields[]` (`description`, `acceptanceCriteria`, `summary`, `components`), `contentDigest` |
| `ap.work.item.commented.v1` | F | Normalizer | Work Comment Parser | `key`, `commentId`, `author`, `bodyRef` |
| `ap.work.item.linked.v1` | F | Normalizer | Work Item Orchestrator | `key`, `linkType` (`blocks`, `relates`), `target` |
| `ap.work.item.deleted.v1` | F | Normalizer | Work Item Orchestrator (cancel) | `key` |

### work.request / guidance / clarification — L2 / L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.work.request.received.v1` | I | Work Intake Router, Command handler | Work Item Orchestrator | `key`, `trigger` (`assignment`, `status`, `label`, `command`, `api`), `sponsor` |
| `ap.work.request.accepted.v1` | F | Work Item Orchestrator | Work Reporter, Projections | `workItemId`, `sponsor`, `team`, `candidateRepos[]` |
| `ap.work.request.rejected.v1` | F | Work Item Orchestrator | Work Reporter | `reason` (`issue-type`, `no-identity-mapping`, `no-repo`, `quota`, `policy`, `too-large`) |
| `ap.work.guidance.provided.v1` | F | Work Comment Parser, Console, CLI, Slack adapter | Work Item Orchestrator, Session Orchestrator | `workItemId`, `author`, `authorRole` (`sponsor`/`team-member`), `channel`, `contentRef`, `scope` (`plan`/`session:<id>`/`all`) |
| `ap.work.clarification.requested.v1` | I | Work Item / Session Orchestrator (from agent) | Work Reporter, Notifier | `clarificationId`, `questions[]`, `blocking` (bool), `expiresAt` |
| `ap.work.clarification.answered.v1` | F | Work Comment Parser, Console | Orchestrators | `clarificationId`, `answeredBy`, `answersRef` |
| `ap.work.clarification.expired.v1` | F | Clarification Manager | Orchestrators | `clarificationId` |
| `ap.work.repos.resolved.v1` | F | Repo Resolver | Implementation Planner (via Orchestrator) | `repos[]{repo,confidence,source}` (`component-map`, `catalog`, `history`, `agent`) |

### work.plan — L3 (implementation plan; distinct from `build.plan`)

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.work.plan.requested.v1` | I | Work Item Orchestrator | Agent Gateway (Implementation Planner) | `workItemId`, `version`, `guidanceRefs[]`, `revisionFeedbackRef?` |
| `ap.work.plan.proposed.v1` | F | Work Item Orchestrator (after agent proposal + validation) | Plan Approval Manager, Work Reporter | `planId`, `version`, `digest`, `planRef`, `repos[]`, `testsPlanned{unit,integration,contract}`, `openQuestions` (count), `estimate{cost,wallclock}`, `scopeEnvelopeRef` |
| `ap.work.plan.revision.requested.v1` | F | Work Comment Parser, Console | Work Item Orchestrator | `planId`, `version`, `requestedBy`, `feedbackRef` |
| `ap.work.plan.approved.v1` | F | Plan Approval Manager | Work Item Orchestrator, Audit | `planId`, `version`, `digest`, `approver`, `approverRole`, `channel` |
| `ap.work.plan.rejected.v1` | F | Plan Approval Manager | Work Item Orchestrator (cancel) | `planId`, `version`, `rejectedBy`, `reason` |
| `ap.work.plan.superseded.v1` | F | Work Item Orchestrator | Session Orchestrator (pause) | `planId`, `version`, `reason` (`work-item-changed`, `deviation`, `manual`) |
| `ap.work.plan.expired.v1` | F | Plan Approval Manager (timer) | Work Item Orchestrator | `planId`, `version` |
| `ap.work.plan.deviation.detected.v1` | F | Scope Guard | Work Item Orchestrator (→ re-approval) | `sessionId`, `kind` (`path-outside-envelope`, `new-repo`, `forbidden-path`, `test-weakened`, `approach-change`), `detailRef` |

### work — L3 (work item outcome)

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.work.blocked.v1` / `ap.work.unblocked.v1` | F | Work Item Orchestrator | Work Reporter, Notifier | `reason`, `blockingSessionId?` |
| `ap.work.completed.v1` | F | Work Item Orchestrator | Work Reporter, Projections (cost, lead time) | `outcome` (`merged`, `abandoned`, `escalated`, `rejected-at-review`), `changes[]`, `cost`, `durations{planning,approval,implementing,review}` |
| `ap.work.cancelled.v1` | F | Work Item Orchestrator | Session Orchestrator, Env Manager | `reason` (`unassigned`, `issue-closed`, `plan-rejected`, `manual`) |

### devsession — L2 / L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.devsession.requested.v1` | I | Work Item Orchestrator | Session Orchestrator | `workItemId`, `planId@version`, `repo`, `budget{usd,iterations,wallclockS}` |
| `ap.devsession.started.v1` | F | Session Orchestrator | Projections, Work Reporter | `sessionId`, `repo`, `branch`, `sandboxId`, `buildPlanId` |
| `ap.devsession.checkpoint.v1` | F (S) | Session Orchestrator | Projections (console timeline) | `sessionId`, `phase` (`tests-authoring`, `implementing`, `iterating`), `filesChanged`, `summary` |
| `ap.devsession.inner_loop.completed.v1` | F (S) | Session Orchestrator | Projections (escape-rate metric) | `sessionId`, `commands[]`, `testsRun`, `passed`, `failed`, `durationMs` |
| `ap.devsession.commit.pushed.v1` | F | Remediation Applier | Feedback Router, Changeset Coordinator | `sessionId`, `sha`, `branch`, `proposalId`, `filesChanged`, `testsAdded` |
| `ap.devsession.change.opened.v1` | F | Session Orchestrator | Feedback Router (ownership index), Work Reporter | `sessionId`, `repo`, `changeNumber`, `draft` |
| `ap.devsession.feedback.received.v1` | I | Feedback Router | Session Orchestrator | `sessionId`, `source` (`pipeline`, `integration`, `gate`, `review`, `guidance`), `trusted` (bool), `eventRefs[]`, `summary` |
| `ap.devsession.iteration.started.v1` | F | Session Orchestrator | Projections | `sessionId`, `iteration`, `triggerEventId`, `failureSignature?` |
| `ap.devsession.ready_for_review.v1` | F | Session Orchestrator | Work Reporter, Changeset Coordinator | `sessionId`, `changeNumber`, `evidenceRef` (AC → tests → results) |
| `ap.devsession.blocked.v1` | F | Session Orchestrator | Work Item Orchestrator, Notifier | `sessionId`, `reason` (`no-progress`, `iteration-limit`, `budget`, `wallclock`, `clarification`, `deviation`, `guardrail`, `access`), `summaryRef` |
| `ap.devsession.resumed.v1` | F | Session Orchestrator | Projections | `sessionId`, `by`, `newBudget?` |
| `ap.devsession.completed.v1` | F | Session Orchestrator | Work Item Orchestrator, Cost | `sessionId`, `conclusion` (`merged`, `abandoned`, `superseded`, `cancelled`), `iterations`, `cost` |
| `ap.devsession.cancelled.v1` | F | Session Orchestrator | Sandbox reaper, Env Manager | `sessionId`, `reason` |

### changeset — L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.changeset.created.v1` | F | Changeset Coordinator | Projections | `changesetId`, `workItemId`, `members[]{repo,sessionId}`, `mergeOrder[]` |
| `ap.changeset.member.updated.v1` | F | Changeset Coordinator | Projections | `changesetId`, `repo`, `changeNumber`, `headSha`, `validation` (`pending`/`green`/`red`) |
| `ap.changeset.ready.v1` | F | Changeset Coordinator | Trigger Router (`changeset-integration`) | `changesetId`, `members[]{repo,headSha,artifacts[]}` |
| `ap.changeset.merge.requested.v1` | I | Changeset Coordinator | Remediation Applier (merge-queue enqueue) | `changesetId`, `nextMember`, `orderIndex` |
| `ap.changeset.merged.v1` | F | Changeset Coordinator | Work Item Orchestrator | `changesetId`, `mergeShas[]` |
| `ap.changeset.partially_merged.v1` | F | Changeset Coordinator | Notifier (p1), Work Item Orchestrator | `changesetId`, `merged[]`, `pending[]`, `failure` |

### environment.ephemeral / integration — L2 / L3

See [15 — Integration Testing](15-integration-testing.md).

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.environment.ephemeral.requested.v1` | I | Run Orchestrator | Ephemeral Env Manager | `envSpecRef`, `strategy`, `services[]`, `ttl` |
| `ap.environment.ephemeral.provisioned.v1` | F | Env Manager | Run Orchestrator | `envId`, `url`, `services[]{name,digest,version}`, `provisionMs` |
| `ap.environment.ephemeral.provision.failed.v1` | F | Env Manager | Failure Classifier, Retry Controller | `reason` (`capacity`, `image`, `migration`, `health-timeout`, `quota`) |
| `ap.environment.ephemeral.ready.v1` | F | Env Manager | Run Orchestrator | `envId`, `healthChecks[]` |
| `ap.environment.ephemeral.torn_down.v1` | F | Env Manager / Env Reaper | Cost projector | `envId`, `reason`, `lifetimeMs`, `cost` |
| `ap.integration.data.seeded.v1` | F | Integration runner | Projections | `envId`, `strategy`, `migrationsApplied[]` |
| `ap.integration.suite.completed.v1` | F (S) | Integration runner | Gate Evaluator, Failure Attributor, Reporter | `envId`, `suite`, `passed`, `failed`, `skipped`, `reportRef`, `controlRun?` |
| `ap.integration.failure.attributed.v1` | F | Failure Attributor | Feedback Router, Reporter | `repos[]`, `services[]`, `testIds[]`, `class`, `confidence`, `evidenceRefs[]` |

---

## Delivery-plane domains

The sections below cover the delivery plane.

## raw — L0

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.raw.github.received.v1` | F | Ingress | Normalizer, Archive | `event` (X-GitHub-Event), `action`, `deliveryId`, `hookId`, `installationId`, `payloadRef`, `headers` (sanitised) |
| `ap.raw.generic.received.v1` | F | Ingress | Normalizer | `sourceSystem`, `payloadRef`, `contentType` |
| `ap.raw.ignored.v1` | F | Normalizer | Scope dashboard | `rawEventId`, `event`, `action`, `reason` (`unsubscribed`, `bot-loop`, `archived-repo`, `no-mapping`, …) |

## tenant — L1

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.tenant.installed.v1` | F | Normalizer | Registry, Reconciler | `installationId`, `account`, `repositorySelection` (`all`/`selected`), `permissions` |
| `ap.tenant.uninstalled.v1` | F | Normalizer | Registry, Orchestrator (cancel all), Cache GC | `installationId` |
| `ap.tenant.suspended.v1` / `ap.tenant.unsuspended.v1` | F | Normalizer | Registry, Orchestrator | `installationId` |
| `ap.tenant.permissions.changed.v1` | F | Normalizer | Registry, Reporter | `accepted`, `permissions` |
| `ap.team.ownership.changed.v1` | F | Registry | Notifier, Projections | `repo`, `previousTeam`, `team`, `source` (`codeowners`/`catalog`/`manual`) |

## repo — L1 / L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.repo.added.v1` | F | Normalizer | Registry, Profiler (via Router) | `repoId`, `fullName`, `defaultBranch`, `visibility`, `isFork`, `isTemplate` |
| `ap.repo.removed.v1` | F | Normalizer | Registry, Orchestrator, Cache GC | `repoId`, `reason` (`deselected`/`deleted`) |
| `ap.repo.renamed.v1` | F | Normalizer | Registry, Reporter | `repoId`, `from`, `to` |
| `ap.repo.transferred.v1` | F | Normalizer | Registry, Tenant isolation | `repoId`, `fromTenant`, `toTenant` |
| `ap.repo.archived.v1` / `ap.repo.unarchived.v1` | F | Normalizer | Registry, Router (mute) | `repoId` |
| `ap.repo.visibility.changed.v1` | F | Normalizer | Policy, Registry | `from`, `to` (affects secret and cache policy) |
| `ap.repo.default_branch.changed.v1` | F | Normalizer | Registry, Router | `from`, `to` |
| `ap.repo.profile.updated.v1` | F (L3) | Profiler | Registry, Planner | `profileVersion`, `languages[]`, `buildSystems[]`, `packageManagers[]`, `testFrameworks[]`, `layout` (`single`/`monorepo`), `projects[]`, `confidence` |
| `ap.repo.manifest.changed.v1` | F (L3) | Orchestrator (from diff) | Registry, Planner (invalidate) | `path`, `oldDigest`, `newDigest`, `valid`, `errors[]` |
| `ap.repo.settings.drift.detected.v1` | S | Reconciler | Policy, Notifier | `setting` (branch protection, required checks…), `expected`, `actual` |

## scm — L1

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.scm.ref.updated.v1` | F | Normalizer | Router | `ref`, `before`, `after`, `forced`, `commits[]` (`sha`, `message` truncated, `authorId`), `changedPaths[]` (truncated plus `pathsRef`), `pusher`, `isDefaultBranch` |
| `ap.scm.ref.created.v1` | F | Normalizer | Router | `ref`, `refType` (`branch`/`tag`), `sha` |
| `ap.scm.ref.deleted.v1` | F | Normalizer | Orchestrator (cancel), Cache GC | `ref`, `refType` |
| `ap.scm.tag.created.v1` | F | Normalizer | Router (release) | `tag`, `sha`, `annotated`, `signed`, `tagger` |

## change — L1

The "change" is a provider-agnostic pull request / merge request.

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.change.opened.v1` | F | Normalizer | Router, Projections | `change{number,title,draft}`, `head{ref,sha,repo}`, `base{ref,sha}`, `isFork`, `labels[]`, `author` |
| `ap.change.updated.v1` | F | Normalizer | Router (supersede plus new run) | `headSha`, `previousHeadSha`, `forced` |
| `ap.change.retargeted.v1` | F | Normalizer | Router | `fromBase`, `toBase` |
| `ap.change.ready_for_review.v1` | F | Normalizer | Router | — |
| `ap.change.converted_to_draft.v1` | F | Normalizer | Router (deprioritise) | — |
| `ap.change.edited.v1` | F | Normalizer | Projections (title/body only, no rebuild) | `fields[]` |
| `ap.change.labeled.v1` / `ap.change.unlabeled.v1` | F | Normalizer | Router (label-driven pipelines) | `label` |
| `ap.change.review.requested.v1` | F | Normalizer | Review agent (via Router) | `reviewer` (user/team/bot) |
| `ap.change.review.submitted.v1` | F | Normalizer | Router, Approval projector | `state` (`approved`/`changes_requested`/`commented`), `reviewer`, `commitSha` |
| `ap.change.review.dismissed.v1` | F | Normalizer | Approval projector | `reviewer` |
| `ap.change.comment.created.v1` | F | Normalizer | Command parser | `commentId`, `bodyRef`, `isReviewComment`, `path?`, `line?` |
| `ap.change.merged.v1` | F | Normalizer | Router, DORA projector | `mergeSha`, `mergeMethod`, `baseRef`, `mergedBy` |
| `ap.change.closed.v1` | F | Normalizer | Orchestrator (cancel), Cache GC | `merged=false` |
| `ap.change.reopened.v1` | F | Normalizer | Router | `headSha` |

## mergequeue — L1

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.mergequeue.entry.checks_requested.v1` | F | Normalizer | Router | `headSha` (speculative merge commit), `headRef` (`gh-readonly-queue/…`), `baseSha`, `changes[]` |
| `ap.mergequeue.entry.destroyed.v1` | F | Normalizer | Orchestrator (cancel) | `reason` (`merged`/`invalidated`/`dequeued`) |

## command — L2

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.command.received.v1` | I | Normalizer (comment parser), API, Console, Reporter (Checks "re-run" button) | Command handler | `command` (`retry`, `retry-failed`, `cancel`, `explain`, `fix`, `skip-gate`, `deploy`, `promote`, `rollback`, `replan`, `quarantine-test`), `args`, `issuer`, `channel` (`comment`/`api`/`console`/`check-rerequest`) |
| `ap.command.accepted.v1` | F | Command handler | Projections, Reporter (react 👍) | `commandId`, `resultingIntentIds[]` |
| `ap.command.rejected.v1` | F | Command handler | Reporter (reply), Audit | `commandId`, `reason` (`unauthorised`, `unknown`, `invalid-state`, `policy`) |

## schedule — L2

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.schedule.fired.v1` | I | Scheduler | Router | `scheduleId`, `cron`, `pipelineKind` (`nightly`, `dependency-scan`, `cache-warm`, `flake-hunt`, `reprofile`), `ref` |

## pipeline — L2 / L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.pipeline.run.requested.v1` | I | Router, Command handler, API | Orchestrator | `pipelineKind`, `trigger{eventId,type}`, `ref`, `headSha`, `baseSha?`, `changeNumber?`, `priority` (`p0-hotfix`…`p4-background`), `concurrencyGroup`, `trustLevel` (`trusted`/`untrusted-fork`/`bot`) |
| `ap.pipeline.run.rejected.v1` | F | Orchestrator | Reporter, Audit | `reason` (`policy`, `quota`, `duplicate`, `repo-muted`, `stale`) |
| `ap.pipeline.run.queued.v1` | F | Orchestrator | Reporter, Projections | `runId`, `position?`, `estimatedStart?` |
| `ap.pipeline.run.started.v1` | F | Orchestrator | Reporter, Projections | `runId`, `planId`, `planSource` (`cached`/`generated`/`manifest`) |
| `ap.pipeline.run.superseded.v1` | F | Orchestrator | Reporter | `runId`, `supersededBy` |
| `ap.pipeline.run.cancel.requested.v1` | I | Command handler, Orchestrator | Orchestrator | `runId`, `reason` |
| `ap.pipeline.run.completed.v1` | F | Orchestrator | Reporter, Router (chained pipelines), DORA, Notifier | `runId`, `conclusion`, `durationMs`, `jobSummary{total,failed,retried,cached}`, `cost`, `artifacts[]`, `diagnosisIds[]` |
| `ap.pipeline.stage.started.v1` / `ap.pipeline.stage.completed.v1` | F | Orchestrator | Reporter | `runId`, `stage` (`plan`, `build`, `test`, `scan`, `package`, `sign`, `publish`, `deploy`), `conclusion?` |
| `ap.pipeline.job.scheduled.v1` | F | Scheduler | Projections | `runId`, `jobKey`, `pool`, `attempt` |
| `ap.pipeline.job.started.v1` | F | Executor | Reporter, Projections | `runId`, `jobKey`, `executorId`, `attempt`, `image`, `arch` |
| `ap.pipeline.job.completed.v1` | F | Executor | Orchestrator, Reporter, Diagnoser (on failure) | `runId`, `jobKey`, `attempt`, `conclusion`, `exitCode`, `durationMs`, `cacheHitRatio`, `logRef`, `reportRefs[]` (JUnit, SARIF, coverage), `annotations[]` (truncated) |
| `ap.pipeline.job.retry.requested.v1` | I | Orchestrator, Remediator, Command | Orchestrator | `runId`, `jobKey`, `reason`, `diagnosisId?` |
| `ap.pipeline.job.lost.v1` | F | Execution plane | Orchestrator (reschedule) | `runId`, `jobKey`, `executorId`, `lastHeartbeat` |

## build — L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.build.plan.generated.v1` | F | Orchestrator (after Planner proposal plus validation) | Registry, Projections | `planId`, `fingerprint`, `planRef`, `jobs` (count), `generator{agent,model,promptVersion}`, `validation{passed,warnings[]}` |
| `ap.build.plan.reused.v1` | F | Orchestrator | Projections (cache ratio) | `planId`, `fingerprint` |
| `ap.build.plan.invalidated.v1` | F | Orchestrator, Registry | Registry | `planId`, `reason` (`manifest-changed`, `build-files-changed`, `toolchain-changed`, `repeated-failure`, `manual`) |
| `ap.build.plan.committed.v1` | F | Remediator | Registry | `planId`, `prNumber` (plan written back to `.agentic/pipeline.yaml`) |

## test — L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.test.results.reported.v1` | F | Executor | Flake detector, Projections | `runId`, `jobKey`, `suites`, `passed`, `failed`, `skipped`, `reportRef` |
| `ap.test.flake.detected.v1` | S | Flake detector / Diagnoser | Remediator, Notifier | `testId`, `evidence` (pass/fail on same SHA), `flakeRate`, `windowRuns` |
| `ap.test.quarantined.v1` / `ap.test.unquarantined.v1` | F | Orchestrator (policy-approved) | Planner, Notifier | `testId`, `reason`, `issueRef`, `expiresAt` |
| `ap.test.selection.computed.v1` | F | Planner / Test-impact analyser | Projections | `runId`, `selected`, `total`, `strategy` (`all`/`impact`/`affected-projects`) |

## diagnosis — L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.diagnosis.completed.v1` | F | Diagnoser | Orchestrator, Remediator, Reporter (annotation/summary), Notifier | `diagnosisId`, `runId`, `jobKey`, `class` (`code`, `test-assertion`, `test-flaky`, `dependency`, `toolchain`, `infra`, `config`, `policy`, `timeout`, `resource`, `unknown`), `confidence`, `summary`, `evidence[]` (log line refs), `suspectCommits[]`, `suggestedActions[]` |
| `ap.diagnosis.feedback.received.v1` | F | Console, comment reactions | Agent eval pipeline | `diagnosisId`, `verdict` (`correct`/`incorrect`), `actor` |

## artifact — L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.artifact.built.v1` | F | Executor | Signer, Scanner | `artifactId`, `kind` (`oci-image`, `npm`, `maven`, `wheel`, `crate`, `nuget`, `go-module`, `binary`, `helm-chart`, `generic`), `name`, `version`, `digest`, `platforms[]` |
| `ap.artifact.sbom.generated.v1` | F | Executor | Scanner, Registry | `artifactId`, `format` (`cyclonedx`/`spdx`), `sbomRef` |
| `ap.artifact.provenance.generated.v1` | F | Provenance service (trusted builder) | Signer, Policy | `artifactId`, `slsaLevel`, `attestationRef` |
| `ap.artifact.signed.v1` | F | Signer | Publisher, Policy | `artifactId`, `signatureRef`, `rekorLogIndex`, `identity` |
| `ap.artifact.scanned.v1` | F | Scanner | Gate evaluator | `artifactId`, `scanner`, `findings{critical,high,medium,low}`, `reportRef` |
| `ap.artifact.published.v1` | F | Publisher | Release, Deployment, Registry | `artifactId`, `registry`, `ref` (e.g. `ghcr.io/acme/api@sha256:…`) |
| `ap.artifact.promoted.v1` | F | Orchestrator | Deployment | `artifactId`, `fromChannel`, `toChannel` |
| `ap.artifact.revoked.v1` | F | Security, Console | Deployment (block), Notifier | `artifactId`, `reason` |

## gate / policy — L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.policy.decision.recorded.v1` | F | Policy Engine | Audit, Projections | `decisionId`, `query` (e.g. `run.allow`, `proposal.autoapply`, `secrets.grant`), `inputDigest`, `result`, `bundleVersion`, `reasons[]` |
| `ap.gate.evaluated.v1` | F | Orchestrator | Reporter (required check), Approval | `runId`, `gate` (`tests`, `coverage`, `sast`, `vuln-threshold`, `license`, `provenance`, `change-risk`), `result` (`pass`/`fail`/`waived`), `waiverId?` |
| `ap.gate.waiver.granted.v1` | F | Command handler | Orchestrator, Audit | `gate`, `scope`, `grantedBy`, `expiresAt`, `justification` |

## approval — L2 / L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.approval.requested.v1` | I | Orchestrator | Notifier, Reporter, Console | `approvalId`, `subjectRef` (proposal, deployment, waiver), `requiredApprovers` (teams, count), `expiresAt` |
| `ap.approval.granted.v1` / `ap.approval.denied.v1` | F | Normalizer (review / deployment protection rule), Console | Orchestrator | `approvalId`, `approver`, `comment` |
| `ap.approval.expired.v1` | F | Orchestrator (timer) | Orchestrator, Notifier | `approvalId` |

## release — L2 / L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.release.requested.v1` | I | Router (tag / merge with release label), Command, Schedule | Orchestrator | `strategy` (`semver-conventional`, `calver`, `manual`), `ref`, `sha` |
| `ap.release.prepared.v1` | F | Release agent (proposal applied) | Approval, Reporter | `version`, `notesRef`, `changes[]` |
| `ap.release.published.v1` | F | Orchestrator / Normalizer (GitHub `release.published`) | Deployment router, Notifier, DORA | `version`, `tag`, `artifacts[]`, `providerReleaseId?` |

## deployment — L2 / L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.deployment.requested.v1` | I | Router, Command, Release flow | Orchestrator | `environment`, `artifacts[]`, `strategy` (`rolling`/`canary`/`blue-green`), `executor` (`internal`/`argocd`/`flux`/`external`) |
| `ap.deployment.started.v1` | F | Orchestrator / CD adapter | Reporter (GitHub deployment status), Projections | `deploymentId`, `environment` |
| `ap.deployment.completed.v1` | F | Orchestrator / CD adapter | DORA, Notifier, Router (promotion chain) | `deploymentId`, `conclusion`, `durationMs` |
| `ap.deployment.rollback.requested.v1` | I | Command, Remediator (on health signal, policy-gated) | Orchestrator | `deploymentId`, `toVersion`, `reason` |
| `ap.deployment.rolled_back.v1` | F | Orchestrator / CD adapter | DORA (change failure), Notifier | `deploymentId`, `fromVersion`, `toVersion` |

## environment — L1 (signal)

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.environment.health.degraded.v1` | S | Monitoring adapter | Remediator, Orchestrator (pause promotion), DORA | `environment`, `service`, `indicator` (SLO / burn rate), `severity`, `sinceDeploymentId?` |
| `ap.environment.health.recovered.v1` | S | Monitoring adapter | Orchestrator | `environment`, `service` |
| `ap.environment.incident.opened.v1` / `…closed.v1` | F | Incident adapter (PagerDuty / Opsgenie) | DORA (MTTR), Remediator | `incidentId`, `services[]`, `severity` |

## dependency — L1 / L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.dependency.upstream.released.v1` | F | Feed adapter (registries, internal packages) | Router (fan-out to dependents) | `ecosystem`, `package`, `version`, `previous`, `isInternal` |
| `ap.dependency.update.proposed.v1` | F | Dependency agent / Renovate / Dependabot (observed) | Router (validation run), Remediator | `prNumber`, `updates[]`, `groupId` |
| `ap.dependency.graph.updated.v1` | F | Profiler | Registry (reverse-dependency index) | `repo`, `dependsOn[]` (internal), `graphRef` |

## security — L1

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.security.vulnerability.alert.opened.v1` | F | Normalizer (`dependabot_alert`), Scanner | Router (remediation), Gate | `alertId`, `advisory` (GHSA/CVE), `severity`, `package`, `manifestPath`, `fixedIn?` |
| `ap.security.vulnerability.alert.resolved.v1` | F | Normalizer | Projections | `alertId`, `resolution` (`fixed`/`dismissed`/`auto_dismissed`) |
| `ap.security.code_scanning.alert.opened.v1` / `…resolved.v1` | F | Normalizer | Gate, Remediator | `alertId`, `rule`, `tool`, `severity`, `location` |
| `ap.security.secret.detected.v1` | F | Normalizer (`secret_scanning_alert`), Executor scanner | Security (rotate), Notifier (p0), Orchestrator (block) | `alertId`, `secretType`, `location`, `validity` |
| `ap.security.advisory.published.v1` | F | Feed adapter (OSV / GHSA) | Router (fan-out to affected repos via SBOM index) | `advisory`, `affected[]` |

## agent — L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.agent.task.assigned.v1` | I | Orchestrator, Work Item / Session Orchestrator | Agent Gateway | `taskId`, `agent` (`profiler`, `build-planner`, `implementation-planner`, `test-author`, `coding`, `diagnoser`, `remediator`, `reviewer`, `release`, `dependency`, `security`), `inputsRef`, `budget{tokens,usd,wallclockS}`, `autonomyLevel` |
| `ap.agent.task.started.v1` | F | Agent Gateway | Projections | `taskId`, `model`, `promptVersion` |
| `ap.agent.proposal.created.v1` | F | Agent Gateway | Orchestrator, Policy | `proposalId`, `taskId`, `kind` (`build-plan`, `implementation-plan`, `commit`, `clarification`, `patch`, `pr`, `comment`, `retry`, `quarantine`, `release-notes`, `version-bump`, `rollback`, `manifest`), `payloadRef`, `confidence`, `rationale`, `riskScore` |
| `ap.agent.proposal.applied.v1` | F | Orchestrator / Applier | Projections, Agent eval | `proposalId`, `appliedAs` (`auto`/`approved`), `resultRef` (commit / PR / comment URL) |
| `ap.agent.proposal.discarded.v1` | F | Orchestrator | Agent eval | `proposalId`, `reason` (`policy-denied`, `low-confidence`, `approval-denied`, `superseded`, `validation-failed`) |
| `ap.agent.task.completed.v1` | F | Agent Gateway | Orchestrator, Cost projector | `taskId`, `conclusion`, `tokens{in,out,cached}`, `cost`, `toolCalls`, `durationMs`, `traceRef` |
| `ap.agent.escalated.v1` | F | Agent Gateway / Orchestrator | Notifier, Console | `taskId`, `reason` (`low-confidence`, `policy`, `budget`, `repeated-failure`, `suspected-injection`), `summary`, `owners[]` |
| `ap.agent.budget.exceeded.v1` | S | Agent Gateway | Notifier, Policy | `scope` (`task`/`repo`/`team`/`tenant`), `limit`, `spent` |
| `ap.agent.guardrail.triggered.v1` | S | Agent Gateway | Security, Audit | `taskId`, `guardrail` (`prompt-injection`, `secret-in-output`, `forbidden-path`, `egress-denied`), `detailRef` |

> Agent tool calls and LLM token streams are **telemetry** (OTel spans), **not** bus events, because they are too high in volume and too low in business value. They are linked by `traceRef`.

## executor — L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.executor.pool.capacity.changed.v1` | S | Autoscaler | Scheduler, Projections | `pool`, `available`, `desired`, `queueDepth` |
| `ap.executor.image.updated.v1` | F | Image pipeline | Planner (toolchain fingerprint), Registry | `image`, `digest`, `toolchains{}` |

## notification — L3

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.notification.sent.v1` | F | Notifier | Audit | `channel` (`slack`/`teams`/`email`/`gh-comment`), `audience`, `templateId`, `relatedEventId` |

## extci — L1 (coexistence)

Used while teams migrate off existing CI. The platform observes, but does not own, these runs.

| Type | Class | Producer | Consumers | Key data |
|------|-------|----------|-----------|----------|
| `ap.extci.run.completed.v1` | F | Normalizer (`workflow_run`, `check_suite` from other apps, `status`) | DORA, Diagnoser (optional), Migration dashboard | `system` (`github-actions`, `jenkins`, `circleci`, …), `name`, `headSha`, `conclusion`, `url`, `durationMs` |
| `ap.extci.check.completed.v1` | F | Normalizer (`check_run` from other apps) | Gate (if required as external input) | `app`, `name`, `conclusion` |

---

## Catalog statistics (v0.2)

- Delivery-plane domains: 24, ~120 types (plus 95 build-process types in [12](12-build-process-events.md))
- Work-plane + integration domains: 6, ~60 types
- Agent-authored changes reuse every delivery-plane event unchanged. The link between planes is `ap.devsession.change.opened` plus correlation `links`.

## Adding a new event type

1. Open a PR to `ap-event-schemas` with the JSON Schema, a catalog entry here, and owner, producer and consumers.
2. Add or update the **scope-matrix** row in [06](06-pipeline-lifecycle.md) if the event triggers anything.
3. Add a mapping in [05](05-source-mappings.md) if it is an L1 event from an external source.
4. A platform-architecture CODEOWNER reviews. Breaking changes need an ADR.
