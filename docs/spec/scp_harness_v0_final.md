# SCP Harness v0.2 — Design

Status: normative design, revised by O5 on 2026-09-23.
The [execution contract](scp_harness_v0_execution_plan.md) contains the mechanical Git, isolation, ledger, worker schema and recovery requirements.

## Authority

RESOURCE IS NOT AUTHORITY. EXECUTION AUTHORITY IS NOT PROMOTION AUTHORITY.
INTERRUPTION IS NOT CANCELLATION. FAILURE WITHOUT USEFUL EVIDENCE MUST NOT AUTO-LOOP.
ATTEMPT IS AGENT EXECUTION; CI IS MACHINE VERIFICATION.

Task/Option express intent and resource ancestry. Claim preserves issuer and created-against provenance. Attempt is exactly one agent invocation. Artifact is an immutable bounded tar snapshot, including unsuccessful and interrupted work. Change is the durable identity of a concrete released development activity. CIRun is independently persisted machine verification evidence.

## Change

One linear stage: MUTATION, CI, REVIEW, AWAIT_PROMOTION or PROMOTION. One state: QUEUED, RUNNING, PAUSED, BLOCKED, STALE, DONE or ABORTED. No dependencies, children, workflow edges, or programmable workflow.

`option release` creates MUTATION/QUEUED at the Task authoritative SHA, with the current objective snapshot. It needs option.release and positive Option budget, grants development authority only, and rejects another nonterminal Change on the same Option. Different Options may be released while activity is running.

Mutation continuation targets its captured Artifact. Submitted work goes through CI and readonly review. PASS + APPROVE enters AWAIT_PROMOTION without changing the repository. Only explicit host `change promote` with change.promote grants the separate authority to enqueue PROMOTION. Scheduler construction + CAS must verify the original base still matches. Drift makes the Change STALE and preserves evidence; no automatic rebase, merge, force or redevelopment.

Pause and attempt interrupt terminate current execution, capture writable state if possible, settle the lease, then retain the original stage and current Artifact in PAUSED. Resume uses that Artifact. Explicit abort permanently abandons the Change while retaining all evidence. Invalid/crashed agent output blocks the Change instead of an evidence-free automatic loop.

Task suspend pauses all nonterminal Changes, settles activity before releasing its repository binding, and preserves history. Task resume marks mismatched nonterminal bases STALE; matching Changes remain PAUSED until explicitly resumed. Task revision appends old/new objective, revision, operator and timestamp; past Change/Attempt/Claim provenance is unchanged.

## CI and review

CI runs in the independent machine environment. It has no actor, role card, semantic output, Claim, Artifact, or promotion authority. Every run records status, start/end, exit, durable bounded stdout/stderr, lease/elapsed time and actual timeout/command/config hash.

ci run independently verifies an existing Artifact. change retry-ci, retry-review and rework operate on the current owned Artifact. Reviewer and rework context contain readable ci.result/result.json, stdout.log and stderr.log, with explicit truncation flags. Rework also sees reviewer findings and the prior Artifact.

FAIL/TIMEOUT are evidence, not infrastructure diagnoses. A second consecutive TIMEOUT in a Change blocks it with REPEATED_CI_TIMEOUT before another reviewer or builder can run. A non-timeout breaks the streak; explicit recovery starts a new recovery epoch without deleting prior evidence. Valid rejection with useful findings may rework; invalid or missing review never invents a verdict.

## Resources and scheduling

Conservation: minted = remaining + outstanding + charged + retired. Only explicit Task create/extend mints. Leases charge actual elapsed wall time, bounded by the grant; uncertain recovery charges the full grant. Discussion consumes Task root; mutation, review and CI consume their Option anchor.

Task root must retain 1200000ms after a downward resource transfer. This is only a transfer floor: root consumption may freely cross it; refunds and settlement are exempt. Descendant allocation may traverse existing ancestry atomically; it cannot change parents or mint.

Global executing activity <= 1. Each Attempt, CI, promotion or exclusive recovery releases the slot at its own end. Control edits and queueing remain available. Priority is explicit host requests, existing Change continuations, new Changes, one-time exploration. No preemption. Pending is only the selected activity projection; it has no lifecycle authority.

Refine/merge create new OPEN Options and record old participants CLOSED/SUPERSEDED. Split does not close its parent. Explicit close distinguishes FULFILLED, SUPERSEDED and ABANDONED. History is never deleted. Completion remains separate from promotion.

## Boundaries

Capabilities determine authority, never role names, model identity, natural language or influence. Workers use fresh isolated workspaces; authoritative .git is inaccessible. Researchers claiming source investigation receive a readonly repository snapshot. All external processes, output, workspace/archive operations and Git subprocess counts remain bounded by the execution contract. Git writes use prepared journals and CAS.

Scheduler configuration is frozen at startup. config show reports disk/effective mismatch and restart_required; each CI records the values it actually used. Restart retains Change position. Recovery never automatically performs an unexecuted promotion CAS.

No DAG, parallel workers/CI, workflow DSL, distributed scheduler, provider abstraction, Web UI, remote PR integration or automatic conflict resolution. Development and acceptance use independent configuration, databases and Artifact/runtime directories; installed v0.1.0 and production databases are not modified.
