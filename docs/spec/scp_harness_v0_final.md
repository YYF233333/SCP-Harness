# SCP Harness v0 — Final Design

Date: 2026-09-20
Status: v0 design frozen

## 1. Goal

SCP Harness is a control system composed of fallible workers. No model, reviewer, human, summary, or formal proof is treated as an oracle.

The system does **not** try to prove that every decision is correct. It tries to prevent any single fallible decision from acquiring unbounded authority, resources, or epistemic monopoly, while preserving paths for alternative hypotheses and later correction.

Two governing principles:

- **Never allow authority monopoly.** A worker cannot turn its own judgment into unbounded compute, execution time, child work, or real-world effect.
- **Never allow epistemic monopoly.** A worker cannot become the sole author of world state, option generation, selection, evaluation, or history.

Correct options may still be missed. Resources may still be assigned poorly. Reviewers and O5 members may still be wrong. v0 accepts these limits explicitly.

---

## 2. Frozen v0 data model

The v0 model is deliberately small.

### Persistent semantic objects

1. **Task** — an O5-created top-level objective bound to one authoritative repository while active.
2. **Option** — a natural-language candidate semantic direction.
3. **Artifact** — an immutable captured state produced by a writable workspace Attempt.
4. **Claim** — a statement issued by an identity about a subject; governance effect depends on issuer identity and policy.
5. **ResourceLedger** — the independent resource-account tree.

### Thin runtime record

**Attempt** is not a semantic planning node. It is only the runtime/audit envelope around one bounded worker invocation:

```text
Attempt {
  id
  operation
  args
  actor_instance
  resource_anchor
  lease
  created_against
  status
}
```

Core uses Attempt only to construct context, meter resources, record provenance, and allow interruption. It must not grow dependencies, semantic children, completion logic, or its own planning graph.

### Everything else defaults to operation/event

Promotion, Review, Finding, Refinement, Split, MergeDecision, SchedulerDecision, continuation state, etc. are operations/events unless future implementation proves they require their own persistent identity, lifecycle, or resource/permission state.

**Complexity brake:** a concept is an event/operation by default. It becomes an object only when independent persistence, lifecycle, or resource/authority state is demonstrably necessary.

---

## 3. Identity and role cards

All actors use the same role-card schema. Role names are provenance labels only; Core never branches on names such as `O5`, `MTF`, or `RAISA`.

The current role-card schema is kept separately in `scp_harness_role_card_v0.md` and contains only fields that mechanically affect execution:

```text
schema_version
id
context
capabilities
limits
influence
```

- `context`: address spaces the identity may read.
- `capabilities`: Core-native state transitions/effects it may request.
- `limits`: hard ceilings.
- `influence`: coefficients consumed by configured aggregation policies.

Model/provider/reasoning effort/prompt/fresh-restart policy are **not** part of the card.

### External model binding

A thin external orchestrator owns a single configurable mapping from operation/profile to role card and model substrate. For example:

```yaml
bindings:
  researcher:
    card: researcher.yaml
    model: gpt-high
  merge_judge:
    card: merge_judge.yaml
    model: luna
  merge_synth:
    card: merge_synth.yaml
    model: luna
  operator:
    card: mtf.yaml
    model: ds-v4-flash
  reviewer:
    card: internal-security.yaml
    model: gpt-high
```

Changing a bad model or adding a new provider changes only this mapping. Core, task history, and role-card semantics remain unchanged.

---

## 4. Task lifecycle and repository ownership

### Create

Only an identity with `task.create` (v0: O5) may create a Task.

`task.create` must explicitly provide:

- objective;
- authoritative repository;
- responsible O5 identity;
- initial resource budget.

Task creation is intentionally a **resource minting boundary**. The explicit O5 create/extend budget authorizes resource minting; Option allocation never authorizes a mutation chain. UI defaults may prefill values but Core never silently supplies budget.

### Repository exclusivity

v0 permits at most one `ACTIVE` Task per authoritative repository.

If the repository is already bound, an unrelated new Task is not silently converted into a child of the current Task. That would fabricate a semantic relationship.

### Suspend / resume

O5 may suspend a Task to run another Task on the same repository.

Suspend mechanically:

- interrupts active Attempts belonging to the Task;
- terminates any in-flight mutation/review/promotion cycle;
- preserves captured immutable Artifacts and all semantic history;
- freezes remaining Task/Option resources;
- releases the authoritative-repository binding.

Resume:

- reacquires the repository if free;
- observes the current authoritative version;
- restores Task eligibility for explicit release/discussion; canceled chains do not restart;
- does not pretend old Options/Claims were created against the new world state.

If frequent `suspend T2 -> run T3 -> resume T2` becomes normal, that is evidence for a future persistent Project/Site root. It is deliberately absent from v0.

### Close

The responsible O5 may issue a qualifying `fulfilled` Claim on the Task. Closing a Task:

- interrupts any remaining active Attempt;
- makes all descendant Options non-runnable;
- ends/reclaims remaining Task resource accounts;
- releases the repository binding.

Open descendant Options are not rewritten as semantically false or abandoned. They remain historical unresolved candidates and may later be referenced by a new Task.

---

## 5. Option model

An Option is a versioned natural-language candidate direction plus provenance.

It may be transformed through operations such as:

- propose;
- refine;
- split;
- merge.

Every transformation produces new Options; existing Options are immutable semantic records.

### Lineage is not an execution DAG

```text
Option lineage != Execution DAG
```

A parent/child relation means only that a worker produced one candidate from another. Children are not requirements, a complete decomposition, or execution ordering.

`split(parent -> children)` does not automatically close or supersede the parent. Resource transfer may leave the parent with zero budget, naturally making it non-runnable; it may later receive budget again.

When an Option-target Attempt starts, Core may mechanically expose direct parents/children and their minimal structural metadata. It does not recursively inject the tree.

The Attempt target and resource anchor remain fixed for the life of the Attempt. Seeing a better child does not allow the worker to silently switch accounting to that child.

### Cross-Task reuse

Semantic lineage may cross Tasks, but resource lineage may not.

An old Option can be cited by a new Task, but cannot directly receive the new Task's budget. The new Task must derive a new Option from it. Each Option therefore has exactly one resource parent.

---

## 6. Option generation and dedup

Raw Option proposals are natural language and permanently retained.

v0 dedup is intentionally simple and fallible:

```text
Raw Options
  -> MergeJudge
  -> groups/partition
  -> MergeSynth
  -> canonical dedup projection
```

### MergeJudge

- sees raw Option IDs/text;
- outputs only a partition/grouping;
- may not rewrite Option content.

Core mechanically verifies that each input Option appears exactly once and no unknown IDs are created.

### MergeSynth

- receives one accepted group and its raw texts;
- produces the canonical working representation;
- may not change group membership.

Raw Options remain authoritative provenance. Canonical text is a working projection, not truth.

v0 uses one fast/cheap worker for MergeJudge and one for MergeSynth. Multiple inconsistent partitions are an explicit post-v0 open question.

Refinement is an Option operation, not a fixed pipeline stage. v0 does not require a special pre-merge refinement phase; implementations may later experiment with cheap clarification before dedup without changing Core.

---

## 7. Resource model

Resources form a conservation-preserving tree **inside each Task**:

```text
Task budget
  -> Option allocation
    -> Attempt lease
```

Task creation/extension is the deliberate external resource-minting boundary controlled by O5.

### Resource anchors

Every resource-consuming operation resolves to one resource anchor.

- discussion charges Task root even when its target Option has no allocation;
- single-route mutation charges that Option;
- Artifact-target work charges the Artifact's semantic-anchor Option;
- new-route exploration charges the Task/nearest parent pool;
- cross-Option compare/merge work charges the resource-tree LCA of the participating anchors.

Example:

```text
T1
├─ unallocated: 15d
├─ D9: 2d
└─ D10: 1d
```

`refine(D9)` charges D9. `mutation(D9)` charges D9; `discussion(D9)` charges T1. `attempt(P31)` charges D9 if `P31.semantic_anchor = D9`. `merge(D9,D10)` charges T1.

### Transfers

Option allocations are real transfers, not duplicated quotas.

- refine with one successor may transfer the parent's remaining allocation;
- split must redistribute the parent's real remaining balance and cannot copy it;
- merge may combine real remaining balances;
- Option close returns unused balance to its parent pool.

Artifacts never own budget.

### Resource dimensions

The ledger may contain multiple dimensions such as wall-clock, CPU, model compute/tokens, tool calls, I/O, or external API usage. Each dimension is metered according to policy; Attempt leases are bounded by both the resource account and the actor's role-card limits.

Waiting and tool execution are not implicitly free merely because the language model is idle. Any resource dimension intended to capture elapsed active time must continue charging while that work is active. Suspending a Task freezes the dimensions configured to pause on suspension.

---

## 8. Scheduling

RESOURCE IS NOT AUTHORITY. Allocation sets the spending ceiling. Only explicit
host `scp option release ID` creates a fresh mutation Pending. Scheduler advances
existing Pending chains/discussions, then one-time initial exploration, then idles.
It never scans funded Options to decide what to implement. No Option can start a
mutation chain without explicit host option.release.

Release requires option.release, OPEN Option, ACTIVE Task, completed exploration,
positive Option budget and no Task Pending. One release authorizes the whole
continuation/test/review/rework/promotion chain. On promotion, DROP, invalid result,
crash, timeout, interruption or changed precondition, Pending is deleted and
execution authorization ends. The Option remains OPEN; another cycle needs another
release even with remaining funds. Recover preserves existing chains and cannot
invent release from historical balances. No persistent release state is added.

Humans propose Options, comment, request bounded discussion, refine immutably,
allocate, then explicitly release. Discussion comments and replies are informational
Claims with exact {"text":"non-empty text"} payloads. Thread is a sorted CLI view.
Discuss atomically adds the comment and a discussion Pending; it requires
option.discuss and claim.publish and is blocked by an existing chain. Comment can
be added during execution. Discussion runs readonly against Task-root budget and
cannot create an Artifact or release. The execution plan specifies the six strict
operation schemas and transaction/error/CLI contracts.

Refine's optional transfer defaults to zero. Refine/split/merge cannot change an
Option participating in Pending; allocation may replenish it. Option text stays
immutable. O5 retains explicit interrupt/close controls.

---

## 9. Context and contamination control

Role-card `context` defines the addressable information space, not the prompt contents.

v0 uses **bounded progressive disclosure**:

```text
Addressable Context >> Working Context
```

Core provides paginated navigation rather than an intelligent all-knowing context selector. Available mechanisms may include:

- structural traversal;
- chronological traversal;
- literal search;
- semantic search;
- relation lookup;
- bounded non-directed sampling;
- explicit inspect/read;
- on-demand summaries.

Top-level listings use mechanically available metadata and are paginated. Summaries are derived views with provenance and never replace raw sources.

All context navigation consumes the current resource anchor's budget.

### `created_against`

Every worker-generated semantic statement records the world state it was based on, including at least relevant authoritative repository/task-state versions.

This applies to Options, Claims, summaries, interpretations, and similar semantic outputs.

Core does not declare an old object stale merely because the repository changed. Later workers are shown origin state vs current state and may inspect the difference.

---

## 10. Core-native APIs vs sandbox execution

Core-native operations are capability-checked per call, e.g.:

- option proposal/refine/split/merge;
- Claim publication;
- resource proposal/allocation operations;
- context navigation;
- Task control;
- Attempt interrupt.

Inside a writable sandbox, v0 **does not** try to classify arbitrary shell/git/compiler/test commands by semantic intent.

The real boundary is provided by:

- filesystem/worktree isolation;
- credentials isolation;
- network policy;
- process sandboxing;
- resource limits.

Candidate code may execute arbitrary code inside this containment. Core does not rely on command-string classifiers for safety.

---

## 11. Attempt execution

Attempt is a thin bounded worker invocation record, not a task graph node.

For cognitive/read-only operations, Attempt records worker, context, lease, created-against state, and termination. Outputs are published through Core-native APIs.

For writable-workspace mutation operations, Core additionally:

1. creates a controlled workspace from the authoritative snapshot or selected Artifact;
2. grants the worker sandbox-local write/execute rights under the lease;
3. freezes the workspace at Attempt termination;
4. mechanically captures the real final workspace state as an immutable Artifact.

Only Attempts with writable workspaces mechanically produce workspace Artifacts.

Termination can be returned, timeout, interrupted, or crashed. None of these automatically means the semantic Option is true/false/fulfilled.

---

## 12. Artifact model and continuation

Artifact means only:

> immutable evidence of the actual writable workspace state captured by Core.

It may be a successful feature, a half-finished implementation, an interrupted mess, or a rejected change. Artifact itself has no open/completed state and no budget.

Artifacts inherit the semantic anchor Option and resource provenance of the mutation chain that produced them.

### Mutation terminal disposition

A normally returning mutation worker may choose one of three v0 dispositions before termination:

- **PROMOTE_FINAL** — capture Artifact, then request promotion/review.
- **CONTINUE_FINAL** — capture Artifact, make it the default target of the next continuation Attempt for the same Option.
- **DROP_FINAL** — capture Artifact for history only; the chain ends; a new host option.release is required to retry from authoritative state.

O5 `attempt.interrupt` defaults to `DROP_FINAL` unless explicitly overridden.

Disposition changes lifecycle only. It does not change the factual meaning of the Artifact or create budget.

---

## 13. Effect promotion and impact-based review

Repository state is divided into impact zones conceptually such as:

```text
private feature/worktree
  -> shared dev
  -> main
  -> release/production
```

The project effect policy defines the required scrutiny when increasing blast radius. This is risk governance, not proof of semantic correctness.

v0 keeps Promotion as an event/operation, not a new persistent object.

A feature Artifact may request promotion. Review is read-only. Reviewer findings and approval/rejection are Claims/events.

The governance meaning of a Claim depends on:

```text
Claim content + issuer identity/card + policy
```

The same apparent approval from an unqualified actor may be informational ("gray check") while a qualifying reviewer satisfies the gate ("green check"). Core does not branch on role names; policy consumes mechanical identity/card properties and configured influence/authority.

### Immutable PR semantics

In v0 the submitted PR/change is the immutable Artifact. The worker that produced it has terminated; reviewers cannot modify it. A rejection never reopens the Artifact.

---

## 14. Protected Test Runner

Test code is untrusted payload. The Test Runner is protected containment + invocation policy.

Protected runner responsibilities include:

- sandbox setup;
- fixed adapter/entrypoint selection;
- resource/time limits;
- result collection;
- interpreting timeout/crash/process-level success/failure.

Feature code may add tests, fixtures, or test data. Poor or extremely slow tests consume the feature's own Option budget and may time out.

The candidate may not silently change runner resource policy or interpretation. Runner-policy changes require a separate higher-impact change path.

Reviewers do not patch missing tests or code in-place. They issue findings/Claims. Remediation occurs in a later bounded mutation Attempt.

---

## 15. Review reject and rework

If an immutable Artifact is rejected:

```text
Artifact Pn
  -> review.reject + findings
  -> new Attempt(target = Pn)
  -> Artifact Pn+1
```

No Researcher is inserted to translate the finding into a directive.

The next worker receives mechanical facts:

- rejected Artifact;
- reject verdict;
- findings/Claims;
- semantic anchor Option;
- current context access;
- a lease from the Option's remaining budget.

The worker decides what to do. If it behaves badly, it consumes bounded budget.

If the Option has no remaining budget, rework cannot start. The slot is released while the exact Pending step remains; O5 may replenish resources to resume that already authorized chain or cancel it.

---

## 16. O5 emergency interrupt

O5 has a direct escape hatch: `attempt.interrupt`.

Core mechanically:

- revokes the current lease;
- requests termination and hard-kills after the configured grace period;
- terminates controlled child processes/tools;
- charges resources already consumed;
- records Attempt as interrupted;
- captures the workspace Artifact if the Attempt had a writable workspace;
- releases the mutation chain/resource lock as appropriate.

`interrupted` does not imply failed, rejected, abandoned, or false.

The Option remains open and keeps its unspent allocation. Only another explicit host option.release permits a fresh worker to retry the same Option. Retry after O5 interrupt starts from authoritative state; the interrupted Artifact remains inspectable history.

---

## 17. O5 completion semantics

Workers may Claim that an Option is fulfilled, but a normal worker Claim is informational unless policy says otherwise.

The responsible O5 is the default v0 authority for semantic completion:

```text
worker: claim(D9, fulfilled)      # evidence/opinion
O5-1:  claim(D9, fulfilled)      # qualifying completion decision
```

Closing an Option returns its unused allocation to the parent pool.

No mandatory vote is required for normal Option completion. This avoids turning routine semantic acceptance into model bureaucracy and recognizes that O5 is closest to project intent while still fallible.

Quorum-style policies are reserved for higher-impact system-policy changes if introduced later.

O5 approval is an authority event, not proof of truth.

---

## 18. Full v0 walkthrough: Vorton

### Step 1 — Task creation

O5-1 creates:

```text
T1 objective: build an agent-native language
repo: vorton
budget: explicitly granted by O5-1
```

Core checks `task.create`, verifies the repository is free, binds it to T1, creates the Task resource root, and starts ACTIVE-task metering.

### Step 2 — Initial exploration

Researcher and independently framed workers run bounded cognitive Attempts charged to T1. They see only their role-card address spaces through bounded progressive disclosure.

They produce raw Options, e.g. benchmark-first, prototype-first, validate-PL-necessity, HM+effects, stop/ask/reframe alternatives. Each is stored with provenance and `created_against`.

### Step 3 — Dedup

One cheap MergeJudge partitions raw proposals. One cheap MergeSynth produces canonical working Options. Raw proposals remain available.

### Step 4 — Allocate

Explicit resource allocation transfers real portions of T1's budget to candidate Options without execution authority. Unallocated Task resource remains available for further exploration/cross-route work.

### Step 5 — Schedule

O5 explicitly releases an Option after discussion/refinement/allocation. The mechanical scheduler executes that Pending chain; allocation alone never starts work.

### Step 6 — Execute

Orchestrator maps the operation/profile to a role card and model binding. Core creates an Attempt and a sandbox worktree from the authoritative repository.

The worker may inspect direct Option lineage and pull more context through navigation APIs. The Attempt target/resource anchor are frozen.

Sandbox commands are arbitrary within containment. Tests run under the protected Test Runner and charge the same Option budget.

### Step 7 — Continue or submit

If work is incomplete but useful, worker returns `CONTINUE_FINAL`; Core captures Artifact X1 and a later Attempt continues from X1.

If the worker thinks the state is ready, it returns `PROMOTE_FINAL`; Core captures Artifact X2 and starts the configured review/promotion path.

If the worker wants to discard the working state, `DROP_FINAL` keeps the Artifact only as history and later work restarts from authoritative state.

### Step 8 — Review

Reviewer is instantiated read-only. It sees the immutable Artifact, relevant target Option, protected test results, and bounded context.

It may publish findings and an approval/rejection Claim. It cannot edit the candidate or add tests itself.

### Step 9a — Reject

Suppose the benchmark harness lacks cross-package scenarios. Reviewer rejects with a finding.

If budget remains, Core mechanically schedules a fresh Artifact-target rework Attempt using the same semantic-anchor Option budget. It supplies the rejection/finding as fact but no smart directive.

The new worker produces a new immutable Artifact. The old Artifact remains frozen.

### Step 9b — Approve and promote

When review policy is satisfied, Core applies the Artifact to the configured higher-impact repository zone. Because v0 holds the single mutation chain across mutate/review/rework/promotion, the authoritative base cannot drift underneath the Artifact.

The chain then releases the write slot.

### Step 10 — Semantic completion

Promotion does not imply the Option is fulfilled. A complex Option may require multiple promotion cycles.

When O5-1 decides the intended semantic result is sufficient, O5-1 publishes the qualifying `fulfilled` Claim. The Option closes and unused budget returns to its parent pool.

### Step 11 — Split and alternatives

A later Option may split into children. Split creates new Options but does not close the parent. Budget is genuinely transferred/reallocated, not copied.

If a parent later runs, it sees its direct children only as known candidate developments, not requirements or an execution plan.

### Step 12 — Bad worker / emergency stop

If a worker burns time with no useful feedback, O5-1 interrupts its Attempt. The worker/process is stopped, consumed resources remain charged, writable state is captured for audit, the Option remains open, and a fresh model instance may retry with the remaining Option budget.

### Step 13 — Emergency unrelated Task

If an urgent compiler bug arrives while T1 is active, O5-1 may suspend T1. All T1 Attempts stop, in-flight promotion is cancelled, budgets freeze, and the repo is released.

O5-1 creates T2 with a separately explicit budget. After T2 closes, T1 resumes against the new repository version. Historical Options/Claims retain their old `created_against` provenance; workers decide whether they still apply.

### Step 14 — Task completion

O5-1 eventually claims T1 fulfilled. Core closes the Task, stops any remaining activity, frees the repo, and ends remaining resource accounts. Unresolved historical Options remain searchable but not runnable.

A future Task may derive new Options from them, creating new resource ownership while preserving semantic provenance.

---

## 19. v0 invariants

1. **Role names have no Core semantics.**
2. **Model/provider choice has no Core authority semantics.**
3. **Every resource-consuming operation has exactly one resource anchor.**
4. **Within a Task, resource transfers conserve allocated resources; only O5 Task create/extend mints new resources.**
5. **Artifacts never own budget.**
6. **Option lineage is provenance, not an execution DAG.**
7. **Every Option has exactly one resource parent.**
8. **A Task-bound repository has at most one ACTIVE Task in v0.**
9. **At most one repository mutation chain is active for that Task/repository.**
10. **Mutation Artifacts are immutable snapshots captured by Core, not worker self-reports.**
11. **Reviewers are read-only; rejection leads to a fresh bounded rework Attempt.**
12. **Test Runner policy is protected; test payload is untrusted and charged to the feature budget.**
13. **Claims gain governance effect from issuer identity/card + policy, not from wording alone.**
14. **O5 interrupt kills an Attempt, not its Option.**
15. **Worker-generated semantic outputs retain `created_against` provenance.**
16. **Addressable context is larger than working context; raw provenance remains reachable.**
17. **No worker can self-mint identity, budget, or external authority.**
18. **No new persistent object type is added unless operations/events demonstrably cannot represent the required independent lifecycle/state.**

---

## 20. Accepted limitations / post-v0 questions

These are not blockers for v0:

- multiple inconsistent MergeJudge partitions;
- parallel repository mutation / merge-conflict handling;
- persistent Project/Site root with sibling Tasks;
- better option-search/exploration policies;
- optimal resource-allocation/worker-scheduling policies;
- richer human/team O5 quorum policies;
- semantic detection of scope creep inside an otherwise authorized bounded Attempt.

The last limitation is fundamental to the design: SCP Harness intentionally does not pretend a mechanical Core can decide arbitrary software-engineering semantic correctness. It bounds the cost and impact of bad judgment and preserves independent correction paths instead.
