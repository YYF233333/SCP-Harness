# SCP Harness Role Card v0 — Mechanical Contract

Status: frozen v0 schema
Date: 2026-09-20

The machine-readable authority is `scp_harness_role_card_v0.schema.json`. This document explains its semantics. If this prose and the JSON Schema disagree, the specification package is inconsistent and implementation/final acceptance must stop; the implementer must not choose one interpretation.

## 1. Principle

Role names are provenance labels only. Core never branches on names such as O5/MTF/reviewer. Model/provider/prompt/reasoning effort are not role-card fields.

All authority is default-deny and comes from explicit `capabilities`. `context` controls read visibility only.

## 2. Exact top-level shape

A v0 card has exactly six fields and no others:

```json
{
  "schema_version": 0,
  "id": "operator",
  "context": ["task.objective","task.state","option.target","artifact.metadata","ci.result"],
  "capabilities": [
    {"name":"task.create"},
    {"name":"option.propose"},
    {"name":"repository.read","scope":"task.repository"},
    {"name":"sandbox.write","scope":"lease.sandbox"},
    {"name":"process.execute","scope":"lease.sandbox"}
  ],
  "limits": {"lease.wall_ms": 600000},
  "influence": {}
}
```

Unknown top-level field, unknown context channel, unknown capability name, unknown limit key, illegal/missing scope, duplicate JSON key, or missing required field is invalid. Duplicate capability names are also invalid even if JSON Schema alone cannot express that cross-item uniqueness by `name`; the production parser must enforce it.

## 3. Context

Allowed v0 channels:

```text
task.objective
task.state
option.target
option.lineage.direct
claim.related
artifact.metadata
artifact.content
ci.result
review.findings
ledger.resource
repository.snapshot
```

A channel not present in `context` is absent from the worker's context projection. `option.lineage.direct` never recursively expands the lineage graph. Context visibility never grants write/effect authority.

## 4. Capabilities

Allowed names are the closed v0 registry defined by the execution plan and JSON Schema. Scope rules are exact:

- `repository.read` -> `task.repository`
- `sandbox.write` -> `lease.sandbox`
- `process.execute` -> `lease.sandbox`
- all other capabilities -> no `scope` field

Capability names are mechanical. `id` does not imply capability.

## 5. Limits

The only v0 limit is:

```json
{"lease.wall_ms": <positive integer>}
```

For every Attempt:

```text
lease_wall_ms = min(resource_anchor.remaining_wall_ms, worker_profile.timeout_ms, role_card.limits["lease.wall_ms"])
```

No CPU/token/tool-call limit is part of v0 Core.

## 6. Influence

`influence` is retained only for conceptual forward compatibility. Values must be non-negative JSON numbers. **v0 Core must not consume influence for capability, resource allocation, scheduling, review, promotion, or completion.** Two otherwise identical v0 cards that differ only in influence must produce identical Core decisions.

## 7. Runtime identity

Workers cannot self-issue or select cards. `actor_instance` and role card are selected by Core from the configured worker profile; worker result cannot override issuer/capabilities/context/limits.

## 8. Human-in-the-loop amendment (2026-09-22)

The closed capability registry includes unscoped `option.release` and
`option.discuss`. The host O5/operator card owns both; normal worker cards never
receive option.release. Release is available only through the host CLI/Core method,
not worker outputs or Claims. Discussion also requires claim.publish.

The sixth operation, discussion, has a separate readonly profile with
synthetic_git=false. Its minimal card contains task.objective, task.state,
option.target, option.lineage.direct, claim.related and ledger.resource; capabilities
are claim.publish, repository.read(scope=task.repository) and
process.execute(scope=lease.sandbox). Its lease is anchored to Task root even if
the target Option has no allocation. It receives no write, allocation, release,
review or completion capabilities. Example: testdata/cards/discussion.json.

## v0.2 host capabilities and CI context

The capability registry adds task.revise, change.promote, change.pause, change.resume, change.abort, change.retry-ci, change.retry-review, change.rework and ci.run. Host CLI authorization checks these explicitly. Worker results cannot invoke any host lifecycle operation; no natural-language request, Claim or verdict grants release/promotion authority.

ci.result is a directory containing result.json, stdout.log and stderr.log. The metadata includes truncation flags and actual verification configuration. Reviewer cards must see ci.result; rework receives CI evidence, reviewer findings and the previous Artifact. Research profiles claiming source investigation require repository.read and readonly workspace.
