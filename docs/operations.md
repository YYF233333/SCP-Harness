# SCP Harness v0 operations

Execution semantics remain governed by `docs/spec/scp_harness_v0_execution_plan.md`.
The O5 test-layering decision supersedes its older Windows-only testing policy.
All Windows development and commands below run in the original repository.

## Daily Linux and Windows release testing

Daily development, CI and protected tests use Linux. In an unprivileged Linux
session (the `scp` user in `SCP-Test`), the default commands are:

```sh
go test ./...
go vet ./...
# Equivalent uncached daily entry point:
sh scripts/test-daily.sh
```

The local fixture builds the ordinary worker executable from
`testdata/workers/main.go` and starts it directly. It uses real Git, SQLite,
filesystem permissions, process groups, CLI subprocesses and crash/recovery.
It never invokes `wsl.exe`. Each fixture has separate `.local-worker` and
`.local-test` directories prefixed by its temporary database filename. These are disposable
test data, not copies of the production Core database or Artifact store.
Run as a non-root user so read-only workspace checks use real Unix permissions.
This local path tests semantics; the Windows/WSL host isolation boundary belongs
to release validation.

Windows-specific test entry points require `-tags=release`. The current release
selection is maintained in [`scripts/final-acceptance.ps1`](../scripts/final-acceptance.ps1):
Windows/WSL boundaries, lifecycle and recovery, authority regressions, observation,
and real Codex integration (`codex_integration`). A plain Windows `go test ./...`
does not start WSL and is not a substitute for the daily Linux suite. Shared test
bodies keep portable lifecycle, settlement and recovery assertions in the Linux
suite. `TestRecoveryProcessHelper` is a subprocess entry point used by crash tests.

All development edits remain in the original Windows repository. To execute its
current files in the isolated test distro, use `scripts/test-daily.ps1`. It streams
a source-only execution snapshot (including uncommitted changes, without `.git`)
to a fresh `/tmp/scp-daily.*` directory in `SCP-Test`; it does not create another
development repository. Stop the normal scheduler first, because it uses the
same dedicated test distro. Logs and the source hash are saved under
`.local/daily/`. The execution snapshot is retained for manual inspection and
cleanup; the script prints its exact path.

## Two dedicated WSL2 environments

The Windows production controller keeps the authoritative repository, SQLite and
Artifact store on Windows. Neither distro receives those production stores.
`SCP-Worker` runs mutation, review, option generation and merge workers.
`SCP-Test` runs protected tests and the daily Linux suite, with its own toolchain.
Only runtimes, required worker credentials and disposable execution files belong
in these distros. Local integration tests create their own temporary Git/SQLite/
Artifact fixtures inside the test environment.

Both `/etc/wsl.conf` files contain:

```ini
[automount]
enabled=false
mountFsTab=false

[interop]
enabled=false
appendWindowsPath=false
```

The existing single global execution slot still serializes all work:
`SCP-Worker mutation -> capture Artifact -> SCP-Test protected test ->
SCP-Worker review -> promotion`. Protected tests restore a fresh writable copy,
record PASS/FAIL/timeout with the existing lease rules, then discard that copy.
They cannot alter the Core-configured command, timeout, policy or interpretation.
Recovery terminates and cleans both environments before releasing the slot.

Use an official Ubuntu rootfs verified against its published SHA-256 to create
`SCP-Test`; do not clone a development or worker distro. The setup script leaves
an existing distro and Go installation in place. Supply a verified Linux amd64 Go
1.26+ archive on first installation; omit the archive arguments when Go is
already provisioned. Windows needs Go, Git and Python.

```powershell
.\scripts\setup-test-wsl.ps1 -Rootfs <official-rootfs> -RootfsSHA256 <published-hash> -GoArchive <linux-amd64-go.tar.gz> -GoArchiveSHA256 <published-hash>
.\scripts\configure-wsl-memory.ps1
```

`setup-test-wsl.ps1` imports only `SCP-Test` if absent, installs Git/Python/tar,
and calls `setup-worker-wsl.ps1` to configure and verify both dedicated distros.
`SCP-Worker` must already exist from its original verified rootfs installation.
The configuration requires distinct `wsl.distro = SCP-Worker` and
`wsl.test_distro = SCP-Test`; add the latter to an existing operator config.
Both use `/scp/attempt` inside their separate filesystems.

The Windows user-level `%USERPROFILE%/.wslconfig` sets the shared WSL2 VM limit:

```ini
[wsl2]
memory=8GB
```

This is one 8 GiB VM cap, not 8 GiB per distro. The memory script backs up the
existing file, preserves other settings (including networking mode), and restarts
WSL to apply the cap. Stop SCP and other active WSL work before running it.
Release validation checks both the setting and Linux-visible memory.

If WSL cannot reach the Go module proxy, run `scripts/install-test-modules.ps1`
from Windows before the daily suite. It verifies the existing Windows module
cache and transfers only the modules pinned in this project's `go.mod` through
stdin. Linux Go still verifies `go.sum`; proxy/TLS settings and drive isolation
remain unchanged. This is dependency provisioning, not a copy of Core state.

Archive attributes are unsupported. Any `export-ignore` or `export-subst` text in
an exact-SHA tracked `.gitattributes` blob (including comments, disabled rules and
macros) rejects that snapshot as `REPOSITORY_UNAVAILABLE`. One bounded exact-tree
Git grep performs this check. Exports preserve blob bytes and ignore ambient Git
configuration and attributes; see specification §§25/29 for the exact rules.

## Configure and run

The [Windows installer](windows-installation.md) exposes the CLI as `scph` and
sets a user-level `SCP_CONFIG` path. All `scp` command examples below also work
with `scph`; an explicit `--config` always takes precedence.

Copy `scp.example.json` to a caller-selected config file and explicitly set
database, Artifact store, card paths, worker commands, protected test command,
all limits and timeouts. Relative host paths resolve against the config directory.
The example's worker commands are installation locations to fill, and its protected
test command is `go test ./...` in `SCP-Test`; provision the toolchain and module cache there before self-hosting. Set an explicit timeout and funded test budget appropriate to the daily suite.
These are examples, not defaults. `scp init` also requires a valid existing config.

The v0 database schema is embedded from [`internal/store/schema.sql`](../internal/store/schema.sql).
`scp init` initializes it transactionally and is idempotent; unsupported versions
fail closed. State updates validate references, resource conservation and immutable
history before committing. There are no external runtime migration files.

Use the fixed, previously accepted controller (`scp.exe`) for normal work.

```powershell
.\scp.exe --config .\scp.json --json init
.\scp.exe --config .\scp.json --json task create --objective "Example objective" --repo C:\path\to\repo --ref refs/heads/main --responsible-actor O5-1 --wall-ms 600000
.\scp.exe --config .\scp.json run
```

Initial exploration creates inert candidate Options once, then the scheduler idles.
Use this daily workflow from another terminal:

```text
option list --task TASK_ID
option thread OPTION_ID
option discuss OPTION_ID --text "Why this plan? What are the risks?"
option thread OPTION_ID
option comment OPTION_ID --text "Additional context"
option refine OPTION_ID --text "Final plan"
option allocate CHILD_ID --wall-ms 7200000
option release CHILD_ID
```

`discuss` queues one bounded readonly reply; normal `scp run` executes it. `comment`
only adds information and works during an active chain. `refine` creates an immutable
child and defaults to zero transfer; `propose` creates a fresh candidate.
**Allocate alone never starts work.** Release requires option.release and approves
one chain through continuation, tests, review, rework and promotion. Promotion leaves
the Option OPEN but idle; another cycle requires another release. DROP/crash/timeout/
interrupt also ends authorization. Recover never infers release from old balances.

Discuss requires option.discuss + claim.publish and Task-root budget, even for an
unfunded Option. It is blocked by a Task's active/pending chain. To redirect active
work: comment, optionally interrupt, wait for settlement, discuss, refine/propose,
allocate, release. Refine/split/merge participants in Pending are blocked; allocate
may replenish an existing chain. Use option close / task close for completion.

`--json` emits one JSON envelope; `attempt watch` rejects it with USAGE_ERROR.
The complete command list,
projections, error codes and capability matrix are frozen in specification §36.

## Observe a live Attempt

```powershell
scp status
scp attempt watch ATTEMPT_ID
scp attempt diff ATTEMPT_ID
scp attempt interrupt ATTEMPT_ID
```

Watch replays the saved stdout/stderr and follows new output until terminal.
PREPARING waits for log creation. Ctrl+C exits only the watcher; multiple watchers
can coexist. Per-stream byte order is preserved; cross-stream timing is best effort.
The stable Attempt stdout_path/stderr_path point to host-owned bounded files that
survive return, crash, timeout and interrupt. Watch never sends input to workers.

Diff shows actual A/M/D paths and bounded textual changes against the Attempt's
immutable input, including its input Artifact for continuations. Large textual
diffs are marked truncated; an oversized path inventory returns LIMIT_EXCEEDED.
It is BEST-EFFORT OBSERVATION: simultaneous file changes or read failures can return
BLOCKED with a retry message. It does not run Git, scripts or worker code, change
workspace/synthetic Git, write authority state, reserve budget, acquire the execution
slot, or interrupt a worker. Readonly/none Attempts reject diff; use Artifact for
terminal mutation results. Observation failures create no runtime blockers.

Humans judge drift and explicitly interrupt if necessary. Then use comment/discuss,
refine/propose, allocate and explicit release; watching or funding never releases work.

## Interrupt, suspend and recover

`attempt interrupt <id>` requests out-of-band termination and waits for the owner
to capture and settle. `task suspend <id>` also cancels pending work and releases
the repository. `task resume <id>` observes the current ref while preserving old
provenance. Task close retires every remaining account balance exactly once.

Task lifecycle changes, Option release/discuss/close and qualifying `fulfilled` Claims share one
non-waiting Core control gate per database and Task ID. Contention
returns `BLOCKED`; it is not retried. Execution capture/settlement remains free to
finish, and ordinary informational Claims remain available. Suspension and close
wait for cancellation and require no active Attempts, leases or execution slot.

If control exits or fails after cancellation, the OS handle is released but the
durable cancellation flag stays. New control requests cannot take over that flag.
Run explicit `scp recover` after the execution/control owners have stopped.
Recovery acquires the same Task gates and clears cancellation only after termination,
capture, settlement and reconciliation. A live controller causes `BLOCKED`.

Ctrl+C stops `run` normally. A stale `run.lock` is deliberately not removed by
`run`; execute `scp recover` after the old process has stopped. Recovery owns the
same global slot, terminates both WSL distros (or local process groups), captures writable interrupted state, charges
uncertain leases fully, and reconciles PREPARED Git journals using the actual ref.
It cleans only Core-owned disposable runtime trees/transfers and the stale lock;
Artifact blobs, Attempt inputs and logs remain audit evidence.

`status` and `blocker list --unresolved` identify infrastructure failures. Repair
the underlying worker/runner/repository condition, then explicitly run
`blocker resolve <id>`. There is no automatic infrastructure retry. Ref drift is
`PRECONDITION_CHANGED`, preserves the Artifact, and does not create a blocker.
SQLite/Artifact durability failures and invariant failures stop all new execution.
If persistence itself fails, the process reports the failure and leaves recovery
necessary; it never treats an unrecorded effect as successful.

## Release verification and controller replacement

A normal promotion requires the daily Linux protected test, not Windows release
acceptance. Keep the previously accepted controller fixed while it develops a
new `main`. At a release or milestone, stop the scheduler and verify the reviewed,
committed source on the actual Windows 11 host with both real WSL2 distros:

```powershell
.\scripts\build.ps1
.\scripts\final-acceptance.ps1 -KnownNormativeFailures none
```

`build.ps1` writes `.local/build/scp.exe`. `final-acceptance.ps1` builds a separate
release candidate under `.local/acceptance/<HEAD>/`, installs fixture executables
in both distros, runs the selection with `-tags 'release codex_integration'`, and records
the source HEAD, binary hash, test log and mapping. Neither script overwrites the
accepted root `scp.exe`. O5 review and a user's explicit replacement follow release
verification; a green daily suite never replaces the controller automatically.

Pass `none` only after reviewing normative conformance as well as tests; otherwise
pass the remaining failures. Unsupported release hosts fail rather than skip or
substitute mocks. Daily Linux evidence is recorded separately and must not be
inferred from the Windows release result.

### Enabling discussion in an existing deployment

Upgrade configuration to six operation profiles using scp.example.json; add the
minimal discussion card and the O5 option.release/option.discuss capabilities.
Existing funded Options without Pending remain safely idle; do not release them
as part of migration. Existing valid Pending chains may recover and finish.

With the normal scheduler stopped, run scripts/enable-codex-discussion.py as root
inside SCP-Worker (pipe its bytes to `wsl -d SCP-Worker -u root --exec python3 -`).
It adds the discussion operation, binding and prompt to /opt/scp-workers/codex-v0
without changing credentials or runtime isolation. Use the installed
/opt/scp-workers/codex-worker executable for the discussion profile. The release
acceptance script requires the real authenticated bundle and runs its model tests;
there is no skip/fake fallback for those cases.

Release source admission rejects tracked edits and non-ignored untracked files.
WSL package tests run sequentially (-p=1) because both distros are shared execution
resources.
