# SCP Harness v0 — Frozen Vorton Acceptance Walkthrough

Status: normative Acceptance A10 fixture
Date: 2026-09-20

This file freezes the **scenario and observations**, not the test harness implementation. Astra may choose how to drive the system, but may not change steps, fake-worker results, transfer amounts, expected repository contents, state transitions, or ledger equations to make the implementation pass.

Actual IDs are random 128-bit hex; the test harness maps them to symbolic labels below. Actual wall-clock charges are measured by production metering and are represented as `C_*`; they need not equal hard-coded durations, but each must satisfy its lease bounds and the global conservation equation.

## 1. Preconditions

- Windows 11 host, real `git.exe`, dedicated WSL2 distro `SCP-Worker`.
- Authoritative local repo target ref `refs/heads/main`, initial SHA = `R0`.
- `R0` tree contains exactly `README.md` with text `base\n` plus any harness-owned fixture metadata explicitly excluded from promotion.
- Operator card has the management capabilities needed by the CLI steps. Worker/reviewer/merge cards have only the capabilities required by their configured operations.
- `total_minted_wall_ms` initially 0 because no Task exists.
- Fake workers are deterministic by operation + target/context and follow §2.

## 2. Frozen fake-worker behavior

### Initial option generation for T1

Return, in this order:

1. `OA`: `benchmark-first`
2. `OB`: `prototype-first-a`
3. `OC`: `prototype-first-b`

MergeJudge returns groups in this exact order:

```json
[["OA"],["OB","OC"]]
```

The harness substitutes actual IDs before writing the worker result. The singleton `OA` receives no MergeSynth. MergeSynth for `[OB,OC]` returns text `prototype-first`; created merged Option is `OM` with zero allocation.

### Mutation/review for OM

1. first mutation from authoritative `R0`: create `stage.txt = "one\n"`; return `CONTINUE_FINAL` -> Artifact `A1`.
2. continuation mutation from `A1`: replace with `stage.txt = "two\n"`; return `PROMOTE_FINAL` -> Artifact `A2`.
3. protected test on `A2`: PASS.
4. reviewer on `A2`: `REJECT`, one finding `missing-final-fix`.
5. rework mutation from `A2`: replace with `stage.txt = "fixed\n"`; return `PROMOTE_FINAL` -> Artifact `A3`.
6. protected test on `A3`: PASS.
7. reviewer on `A3`: `APPROVE`.
8. promotion of `A3`: CAS `R0 -> R1`. `R1` must contain `README.md=base\n` and `stage.txt=fixed\n`.

### Bad-worker path

For child Option `OBAD`, mutation writes `bad.txt = "interrupted\n"`, spawns a child/grandchild process that does not exit, then blocks forever without a final result.

### Initial option generation for T2

Return exactly one Option `OE`: `emergency-hotfix`. No MergeJudge/MergeSynth is run for a one-option batch.

Mutation for `OE` from authoritative `R1`: create `hotfix.txt = "emergency\n"`, return `PROMOTE_FINAL`; protected test PASS; reviewer APPROVE; promotion CAS `R1 -> R2`. `R2` preserves `stage.txt=fixed\n` and adds `hotfix.txt=emergency\n`.

## 3. Frozen scenario and checkpoints

### CP0 — Create T1

CLI semantic operation:

```text
task create objective="build an agent-native language" repo=<fixture repo> ref=refs/heads/main responsible_actor=<operator> wall_ms=600000
```

Expected:

- symbolic Task `T1`, status ACTIVE, current authoritative SHA `R0`;
- exactly one Task root account, remaining=600000;
- minted=600000, charged=0, outstanding=0, retired=0;
- repository bound to T1;
- one initial-exploration runtime marker pending.

### CP1 — Initial exploration/dedup T1

Run scheduler until the one-time initial exploration chain completes.

Expected:

- raw Options `OA`,`OB`,`OC` exist and remain readable; all initial allocation=0;
- merged `OM` exists, parents `[OB,OC]`, allocation=0;
- no additional merged Option for singleton `OA`;
- generation/judge/synth charges are `C_gen1`,`C_judge1`,`C_synth1` against T1 root;
- root remaining = `600000 - C_gen1 - C_judge1 - C_synth1`;
- no participant balance was transferred by MergeSynth;
- initial exploration marker is terminal and cannot run again after restart.

### CP2 — Explicit allocation

Operator transfers:

```text
OM += 240000 from T1 root
OA += 120000 from T1 root
```

Expected exact transfer effect: OM remaining increases by 240000, OA by 120000, root decreases by 360000, minted unchanged.

### CP3 — OM continuation/reject/rework/promotion

Run scheduler through the frozen OM chain in §2.

Expected:

- `A1`,`A2`,`A3` are three distinct immutable Artifacts;
- continuation used fresh workspace from A1; rework used fresh workspace from A2;
- protected test/reviewer sequence exactly PASS/REJECT/PASS/APPROVE;
- promotion occurs only for A3 and creates `R1`;
- `A2` rejection never modifies A2;
- OM remains OPEN after promotion;
- all worker/test/review charges are charged to OM resource anchor;
- `R1` does not contain `bad.txt`.

### CP4 — Semantic completion of OM

Operator executes qualifying `option close OM`.

Expected:

- OM -> CLOSED;
- all OM unused remaining returns to its immediate resource parent (T1 root);
- no retired resource is created by Option close;
- promotion is not itself the completion event.

### CP5 — Split OA

Split OA with exact child allocations:

```text
OBAD = 40000
OSPARE = 30000
```

Because OA had exactly 120000 and has not executed, expected after split:

```text
OA.remaining = 50000
OBAD.remaining = 40000
OSPARE.remaining = 30000
```

Parent remains OPEN; minted unchanged.

### CP6 — Bad worker interrupt

Allow scheduler to start mutation for OBAD and wait until the child/grandchild process exists, then execute `attempt interrupt <current>`.

Expected:

- WSL distro terminates; worker child/grandchild no longer exists;
- Attempt terminal status INTERRUPTED;
- writable state is captured as immutable Artifact `ABAD` containing `bad.txt=interrupted\n`;
- OBAD remains OPEN;
- default disposition is DROP; no continuation/promotion is pending;
- consumed/uncertain charge follows §8.5; conservation holds.

### CP7 — Suspend T1

Operator suspends T1.

Expected:

- T1 -> SUSPENDED; repo binding released; all balances frozen;
- current/pending T1 chain absent;
- `R1` unchanged; all old `created_against` values remain unchanged.

### CP8 — Emergency T2

Create T2 on the same repo with initial budget 120000. Run its one-time option generation; it yields only `OE`. Allocate exactly 80000 to OE, then run OE through mutation/test/review/promotion.

Expected:

- T2 owns repo while ACTIVE;
- no MergeJudge/MergeSynth for one generated Option;
- promotion creates `R2` from `R1`;
- `R2` contains README, `stage.txt=fixed\n`, `hotfix.txt=emergency\n`, and not `bad.txt`;
- OE remains OPEN until explicit close.

Close OE, then qualifying `task close T2`. At T2 close:

- every T2 resource account remaining becomes 0;
- their total prior remaining is added exactly once to T2 `retired_wall_ms`;
- T2 conservation equation still holds;
- repo binding released.

### CP9 — Resume and close T1

Resume T1.

Expected:

- T1 -> ACTIVE, repo rebound, `current_authoritative_sha = R2`;
- historical T1 Options/Claims/Artifacts created against `R0/R1` keep those original values.

Then qualifying `task close T1`. Expected:

- active/pending execution absent; all descendant Options non-runnable;
- every T1 resource account remaining becomes 0;
- sum of all pre-close remaining is added exactly once to T1 `retired_wall_ms`;
- T1 -> CLOSED and repo becomes free;
- T1 conservation equation holds with `remaining=0,outstanding=0`;
- authoritative repo remains `R2`.

## 4. Final invariant checks

For each Task independently:

```text
total_minted_wall_ms
= sum(all account remaining_wall_ms)
+ outstanding_attempt_lease_wall_ms
+ total_charged_wall_ms
+ retired_wall_ms
```

At final CP9, T1 and T2 have `remaining=0` and `outstanding=0`; therefore `minted = charged + retired`. No operation except the two Task creates minted resource.

Also verify:

- at no point were two execution activities concurrent;
- all promotion updates were CAS;
- raw Options and rejected/interrupted Artifacts remain readable history;
- no role name/model/provider string acquired Core semantics;
- exact worker/test/review behavior above was obtained through ordinary configured fake executables, not test-only bypasses in production Core.
