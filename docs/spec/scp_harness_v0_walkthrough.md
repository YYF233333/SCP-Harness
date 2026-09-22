# SCP Harness v0.2 — Vorton acceptance walkthrough

Status: normative fixture updated by the O5 v0.2 ruling. Real Git, SQLite, worker processes, immutable capture, CI, readonly review, cancellation and CAS remain required.

## CP0–CP2: intent, discussion, refinement, allocation

Create T1 with 1800000ms, authoritative R0 containing README.md=`base\n`. One-time exploration generates benchmark-first, prototype-first-a, prototype-first-b; partition/synthesis produces prototype-first. Discussion reads readonly source and produces a reply without an Artifact or release. Refine prototype-first into a new OPEN Option; original becomes CLOSED/SUPERSEDED. The new descendant is the subsequent prototype anchor.

Transfer 240000ms to that descendant atomically through its existing resource ancestry; transfer 120000ms to benchmark-first. Root decreases by 360000ms in addition to real Task-level consumption, never below 1200000ms because of transfer. Allocations alone remain idle. Explicit option release creates the prototype Change at R0.

## CP3: immutable continuation, review and separate promotion

First mutation creates A1 with stage.txt=`one\n`, CONTINUE_FINAL. Next mutation from A1 creates A2=`two\n`, PROMOTE_FINAL. CI PASS; reviewer REJECT with missing-final-fix. Rework from A2 produces A3=`fixed\n`; CI PASS; reviewer APPROVE.

Change must be AWAIT_PROMOTION and authoritative ref still R0. Only after explicit change promote may the scheduler CAS R0->R1. A1/A2/A3 remain immutable; R1 contains README.md and final stage.txt. Change=DONE, Option remains OPEN, and repeated polling starts no second development cycle.

## CP4–CP6: completion, split and interruption

Explicit FULFILLED close refunds prototype balance to its immediate resource parent, independently of promotion. Split benchmark-first into bad-worker (40000ms), spare (30000ms), leaving 50000ms in the parent.

Release bad-worker. It writes bad.txt=`interrupted\n`, starts child/grandchild processes, then blocks. attempt interrupt terminates all descendants, captures ABAD, settles its lease, records INTERRUPTED and retains the Change in MUTATION/PAUSED with current Artifact ABAD. It does not authorize a new release. Explicit change resume must start from ABAD.

## CP7–CP9: suspend, another Task, stale baseline

Suspend T1; retain all nonterminal Changes as PAUSED and release binding after settlement. Create T2 with 1320000ms on the same repository; one-time exploration yields emergency-hotfix. Allocate 80000ms, explicitly release, mutate hotfix.txt=`emergency\n`, CI PASS, readonly APPROVE. Verify R1 is unchanged until explicit change promote; CAS R1->R2 preserves stage.txt and adds hotfix.txt.

Close T2 and resume T1. T1 observes R2, and any nonterminal Change based on R0/R1 becomes STALE, without transplantation or automatic work. Existing Option/Claim/Artifact provenance remains unchanged. Close T1; minted = charged + retired and remaining/outstanding = 0 for both Tasks. Final global slot is IDLE.

## Additional v0.2 acceptance

A–M in the execution contract additionally cover: promotion capability denial, pause/resume with a real captured Artifact, restart, standalone CI without an Attempt, readable CI logs for reviewer/rework, two-timeout fuse, root transfer versus consumption, discussion priority, multiple released Changes with stale detection, configuration provenance/freeze, short IDs and early CLI validation, and SUPERSEDED reasons.

Real Codex release acceptance uses readonly source investigation, discussion, writable mutation, independent CI, readonly review and two separate explicit promotion commands. Workers must demonstrate the OS permissions using the unchanged boundary probe; no provider sandbox substitutes for Harness isolation.
