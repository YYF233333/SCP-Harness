# Worker protocol v0

The executable specification is [the execution plan](spec/scp_harness_v0_execution_plan.md).
The role-card schema and frozen walkthrough in `spec/` remain authoritative.
The execution plan includes the Claim, human-release and live-observation amendments;
`MANIFEST.sha256` hashes the current specification files.

## Production role separation

Core remains provider-opaque. Role names are provenance labels; authority comes
from role-card context and capabilities.

- `option_generation`, `merge_judge`, and `merge_synth` use an Explorer profile
  with `workspace=readonly` and `repository.read` for source investigation.
- `discussion` uses a separate readonly authoritative-source profile with
  synthetic_git=false and Task-root resource anchor.
- `mutation` uses a Builder profile with a writable synthetic workspace.
- `review` uses a Reviewer profile with a read-only submitted Artifact workspace
  and visibility of the CI evidence.

Provider, model, prompt, and reasoning bindings are deployment concerns outside
Core. These profiles do not change the normative specification.

Each configured executable runs as the unprivileged `scp` user in the dedicated
`SCP-Worker` WSL2 distro. CI run separately in `SCP-Test`. Core supplies these environment variables to worker Attempts:

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
mutation results still retain a bounded capture when possible, with the Change BLOCKED; no evidence-free auto retry.
Every workspace use is a fresh copy. Read-only reviewers cannot patch it. CI receive a separate writable copy; their output files never enter promotion.

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

Core creates host-owned stdout/stderr files before worker launch. Its bounded pipe
capture writes them while running; overflow still drains/discards beyond the
existing per-stream cap. Workers cannot write these host files. Attempt paths are
stable during PREPARING/RUNNING, and retained after return, crash, timeout or
interrupt. No new worker fields or provider protocol are involved.
See [live observation](operations.md#observe-a-live-attempt) for operator commands.

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
starts work. Only explicit host option.release creates a new Change. CI PASS + review APPROVE enters AWAIT_PROMOTION; explicit host change.promote is required before repository CAS.

## CI evidence and interruption (v0.2)

ci.result is a directory, not a host path: result.json, stdout.log and stderr.log are bounded actual content. result.json includes stdout_truncated, stderr_truncated and effective timeout/command/config hash. Rework also receives review.findings and the previous Artifact. CI is a CIRun, never an agent Attempt, and produces neither Claim nor Artifact.

An interrupted mutation is captured and its Change pauses at the same stage. Resume restores the current Artifact. Agent failure/invalid output without useful evidence blocks instead of looping; repeated CI TIMEOUT blocks before further review/rework. Only explicit abort abandons the Change.

The runner removes inherited SCP_* runtime variables before each launch. Worker Attempts then receive fresh SCP_INPUT/SCP_CONTEXT/SCP_WORKSPACE/SCP_RESULT. CI receives none of these worker variables. Go and other fixed toolchains must be preinstalled in their execution environments, never installed per Attempt.
