# 11 — Open Questions

> The demo makes simplifying choices for several of these (SQLite instead of a bus for Q1, GitHub Actions as the executor for Q3, compose-in-job for Q20). Those are stand-ins, **not decisions**. See [16 §5](16-implementation-status.md#5-demo-stand-ins-vs-the-design).

| # | Question | Options | Leaning | Owner |
|---|----------|---------|---------|-------|
| Q1 | Bus technology | Kafka-compatible (Redpanda / MSK) vs NATS JetStream vs cloud-native pub/sub | Kafka-compatible (replay, ecosystem, partition ordering). See ADR-0002. | Platform |
| Q2 | Do we execute deployments or only orchestrate external CD? | Internal deployer vs Argo CD / Flux adapters | Adapters first; internal deployer only for simple targets | Platform + SRE |
| Q3 | Coexistence with GitHub Actions | Observe-only (`extci`) vs run Actions-compatible workflows vs migrate | Observe and offer migration PRs. Don't emulate Actions. | Product |
| Q4 | Step library vs arbitrary `run:` | Vetted `uses:` steps only vs free shell | Both. `run:` allowed in manifests (human-authored); agents restricted to the step library plus validated commands. | Security |
| Q5 | Granularity of build-process events on the bus | Per test case vs summaries only | Capped per-case failures (50/job) plus summaries. See 12 §3. | Platform |
| Q6 | Fork PR policy | Never run vs run untrusted vs require maintainer approval | Run untrusted (no secrets) for org members' forks; approval for first-time external contributors | Security |
| Q7 | Mainline batching default | Build every commit vs batch | Batch on, with automatic bisect | Platform |
| Q8 | Plan ownership | Generated plan authoritative vs manifest must be merged | Generated is authoritative until a manifest exists; manifest wins | Product |
| Q9 | Event schema format | JSON Schema vs Avro vs Protobuf | JSON Schema (CloudEvents JSON) for v1; revisit for high-volume streams | Platform |
| Q10 | Reverse-dependency discovery | SBOM-derived vs build-observed (`dependency.internal.consumed`) vs catalog | Build-observed plus SBOM union | Platform |
| Q11 | Autonomy defaults per org | L1 vs L2 | L1 default, L2 opt-in per team, L3/L4 per class allow-list | Product + Security |
| Q12 | Multi-SCM timeline | GitHub only vs GitLab in v1 | GitHub only. Keep L1 provider-agnostic. | Product |
| Q13 | Model provider strategy | Single provider vs routing | Gateway abstracts it; one primary provider plus fallback | Platform |
| Q14 | Handler deployment unit | One service per handler vs grouped services | Group by category (13 §3) and latency class; handlers stay logically separate | Platform |
| Q15 | Can plan approval ever be waived? | Never vs org-policy allow-list for trivial task classes (e.g. dependency bumps, copy changes) | **Decided: required by default** (ADR-0007). A future org-level allow-list is possible, but out of scope for v1. | Product |
| Q16 | Who can approve a plan when the sponsor is away? | Sponsor only vs any owning-team member vs explicit delegate | Explicit delegate via `/ap sponsor @user`, or any member of the owning team after 2 business days | Product |
| Q17 | Jira ↔ GitHub identity mapping source | SSO/SCIM vs mapping table vs email match | SSO/SCIM, with a table override. Never email matching alone. | Security |
| Q18 | Plan granularity | File-level vs component-level scope envelope | Path globs per repo; file lists are informative | Platform |
| Q19 | Dev sandbox suspend/resume | Keep VM warm vs snapshot vs re-provision from branch | Snapshot while waiting on CI under 30 min, otherwise re-provision | Platform |
| Q20 | Ephemeral environment default strategy per system | Baseline + overlay vs full stack | Baseline + overlay where a service mesh exists; compose-in-job otherwise | Platform + SRE |
| Q21 | Multi-repo merge atomicity | Ordered merges + hold vs a "merge train" across repos | Ordered merges through each repo's merge queue, with partial-merge hold and escalation | Platform |
| Q22 | Task sizing / eligibility | Hard limits (repos, estimated files) vs planner judgement | The Planner flags `too-large` and proposes a split into sub-tasks for the developer to create | Product |
| Q23 | Plan approval UX | Jira comment commands vs console vs both | Both. The console shows a diff between plan versions. | Product |
