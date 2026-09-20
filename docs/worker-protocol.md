# Worker protocol v0

The executable specification is [the execution plan](spec/scp_harness_v0_execution_plan.md).
The role-card schema and frozen walkthrough in `spec/` remain authoritative.
The execution plan includes the O5 council's 2026-09-21 Claim clarification;
`MANIFEST.sha256` hashes the current authority files. The original input archive's
execution-plan hash was `c99d4c1c3dfe742c2c012e1481bc58aeb8742b7bad141d3c8cf5392c000c6e23`.

Each configured executable runs as the unprivileged `scp` user in the dedicated
`SCP-Worker` WSL2 distro. Core supplies these environment variables:

| Variable | Path |
| --- | --- |
| `SCP_INPUT` | `/scp/attempt/input.json` |
| `SCP_CONTEXT` | `/scp/attempt/context` |
| `SCP_WORKSPACE` | `/scp/attempt/workspace` |
| `SCP_RESULT` | `/scp/attempt/result.json` |

Read `input.json` to determine the operation, target, frozen actor card and
created-against state. The current directory is the workspace when one exists,
otherwise `/`. Write the precreated `result.json` directly (open/truncate/write),
then exit. Its parent directory and input are owned by Core; input and context are
read-only. There is no runtime RPC, SDK, provider adapter or identity override.

Context channels are literal `<channel>.json` files. `artifact.content` and
`repository.snapshot` are directories. Missing channels are inaccessible.
`option.target.json` contains an array: the target Option, or merge participants
in their frozen input order. Direct lineage contains only immediate neighbours.
Artifact-content projections are provided only for Artifact-target operations.

All result schemas are strict: duplicate/unknown keys, missing/null required
fields, wrong-case enums and blank text fail validation. The five exact schemas
are in specification §14. A minimal mutation result is:

```json
{"schema_version":0,"operation":"mutation","disposition":"DROP_FINAL","claims":[],"new_options":[]}
```

The other dispositions are `CONTINUE_FINAL` and `PROMOTE_FINAL`. Missing/invalid
mutation results still retain a bounded capture when possible, with DROP semantics.
Every workspace use is a fresh copy. Read-only reviewers cannot patch it. Protected
tests receive a separate writable copy; their output files never enter promotion.

Workers may request Claims but cannot choose issuer, provenance, allocation or
new-object IDs. Exact `resource.propose` requires both `claim.publish` and
`resource.propose`; denial rejects that request rather than downgrading it.
It never changes resource balances. Exact `fulfilled` retains the specification's
capability-qualified completion behaviour. Other free-text types do not imply
review, promotion, completion or resource effects.

Configured reserved exit codes indicate `WORKER_UNAVAILABLE`. Ordinary nonzero
exits are crashes. Core always terminates the distro before capture, including on
normal exit, so descendants cannot modify submitted state. Regular files and
directories are supported; links and special files fail closed. A synthetic Git
repository contains one base commit, no remotes, and is excluded from capture.
