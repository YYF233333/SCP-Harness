# Worker protocol v0

The executable specification is [the execution plan](spec/scp_harness_v0_execution_plan.md).
The role-card schema and frozen walkthrough in `spec/` remain authoritative.
The execution plan includes the O5 council's 2026-09-21 Claim clarification;
`MANIFEST.sha256` hashes the current authority files. The original input archive's
execution-plan hash was `c99d4c1c3dfe742c2c012e1481bc58aeb8742b7bad141d3c8cf5392c000c6e23`.

## Production role separation

Core remains provider-opaque. Role names are provenance labels; authority comes
from role-card context and capabilities.

- `option_generation`, `merge_judge`, and `merge_synth` use an Explorer profile
  with `workspace=none`.
- `discussion` uses a separate readonly authoritative-source profile with
  synthetic_git=false and Task-root resource anchor.
- `mutation` uses a Builder profile with a writable synthetic workspace.
- `review` uses a Reviewer profile with a read-only submitted Artifact workspace
  and visibility of the protected-test result.

Provider, model, prompt, and reasoning bindings are deployment concerns outside
Core. These profiles do not change the normative specification.

Each configured executable runs as the unprivileged `scp` user in the dedicated
`SCP-Worker` WSL2 distro. Protected tests run separately in `SCP-Test`. Core supplies these environment variables to worker Attempts:

The [execution boundary](execution-boundary.md) is enforced by Core and the OS.
Provider sandbox/approval profiles have no role in SCP workspace permissions.

| Variable | Path |
| --- | --- |
| `SCP_INPUT` | `/scp/attempt/input.json` |
| `SCP_CONTEXT` | `/scp/attempt/context` |
| `SCP_WORKSPACE` | `/scp/attempt/workspace` |
| `SCP_RESULT` | `/scp/attempt/result.json` |

For native Linux integration, the same fields and environment variables contain
absolute paths under the fixture-local execution directory. Workers must read
the supplied paths instead of assuming `/scp/attempt`. No result schema changes.

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
fields, wrong-case enums and blank text fail validation. The six exact schemas
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
exits are crashes. Core always terminates the worker distro before capture, including on
normal exit, so descendants cannot modify submitted state. Native Linux execution
uses process-group termination; explicit recovery also terminates recorded groups
left by a crashed Core. Regular files and
directories are supported; links and special files fail closed. A synthetic Git
repository contains one base commit, no remotes, and is excluded from capture.

## Live observation

Core creates host-owned stdout/stderr files before worker launch. Its bounded pipe
capture writes them while running; overflow still drains/discards beyond the
existing per-stream cap. Workers cannot write these host files. Attempt paths are
stable during PREPARING/RUNNING, and retained after return, crash, timeout or
interrupt. No new worker fields or provider protocol are involved.

`scp attempt watch ATTEMPT_ID` replays available output and follows to terminal;
Ctrl+C only exits the watcher. It has no stdin/instruction channel. Each stream's
byte order is retained; cross-stream ordering is best effort. `--json` is rejected.
`scp attempt diff ATTEMPT_ID` reads a bounded capture of the live writable workspace
against its immutable input, including continuation Artifact input. It excludes
synthetic Git and never invokes candidate code or Git. A/M/D and bounded textual
hunks are observational, not a published snapshot. Concurrent changes or read
failures return a retry error without touching authority or stopping the worker.
Readonly/none Attempts reject diff; terminal mutation results remain Artifacts.

## Discussion and host release

A discussion result is exactly:

```json
{"schema_version":0,"operation":"discussion","text":"non-empty response"}
```

Read context/discussion-instruction.txt and the current human question in the
ordered claim.related projection. Explain the conclusion, reasons summary, risks
and recommendations; do not output hidden chain-of-thought or claim to have changed
code/Option. O5 decides whether to refine. The readonly worker gets task/objective,
Option/direct lineage/related Claims and ledger channels through its minimal card.

Core publishes a valid RETURNED result as discussion.reply with Attempt-bound
issuer/provenance. Invalid/crash/timeout/interrupt yields no reply, ends the request
and never retries automatically; the human comment remains. Discussion cannot
produce Artifacts, new Options, allocations, release, patch or verdict.

No worker result, new_options entry or Claim can release an Option. Funding never
starts work. Only explicit host option.release creates a fresh mutation Pending;
review APPROVE can only promote the current Artifact in that already released chain.
