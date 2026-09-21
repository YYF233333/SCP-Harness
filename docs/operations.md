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

Windows-specific test entry points require `-tags=release`. The release script
selects only the nine platform boundary tests listed in
[test-migration.md](test-migration.md). A plain Windows `go test ./...` does not
start WSL and is not a substitute for the daily Linux suite. Shared test bodies
keep portable lifecycle, settlement and recovery assertions in the Linux suite.

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
Git grep performs this check; no attribute parser or per-path evaluation exists.
Grep and archive use temporary Core-owned Git metadata referencing the original
object directory. Original repository `info/attributes`, local/user/system Git
configuration and ambient Git overrides are not used. Global/system attributes are
disabled explicitly. A fixed private byte-preservation policy disables checkout
conversion, so ordinary text/EOL rules cannot change the exported blob bytes.
No repository content or worktree is cloned/copied to establish this environment.

## Configure and run

Copy `scp.example.json` to a caller-selected config file and explicitly set
database, Artifact store, card paths, worker commands, protected test command,
all limits and timeouts. Relative host paths resolve against the config directory.
The example's worker commands are installation locations to fill, and its protected
test command is `go test ./...` in `SCP-Test`; provision the toolchain and module cache there before self-hosting. Set an explicit timeout and funded test budget appropriate to the daily suite.
These are examples, not defaults. `scp init` also requires a valid existing config.

Use the fixed, previously accepted controller (`scp.exe`) for normal work.

```powershell
.\scp.exe --config .\scp.json --json init
.\scp.exe --config .\scp.json --json task create --objective "Example objective" --repo C:\path\to\repo --ref refs/heads/main --responsible-actor O5-1 --wall-ms 600000
.\scp.exe --config .\scp.json run
```

Initial exploration creates zero-funded Options once. From another terminal,
inspect `option list --task <id>` and explicitly `option allocate <id> --wall-ms N`.
The scheduler is serial and retains a mutation/test/review/promotion chain until
it ends or must pause. A successful promotion leaves the Option OPEN.
Use explicit `option close` and `task close` for semantic completion.

`--json` emits one JSON envelope. Logs go to stderr. The complete command list,
projections, error codes and capability matrix are frozen in specification §36.

## Interrupt, suspend and recover

`attempt interrupt <id>` requests out-of-band termination and waits for the owner
to capture and settle. `task suspend <id>` also cancels pending work and releases
the repository. `task resume <id>` observes the current ref while preserving old
provenance. Task close retires every remaining account balance exactly once.

Task lifecycle changes, Option close and qualifying `fulfilled` Claims share one
non-waiting Core control gate per database and Task ID. Contention
returns `BLOCKED`; it is not retried. The Windows gate uses atomic creation of a
named kernel mutex and retains only the first creator's non-inheritable handle;
handle lifetime provides exclusion without thread ownership or Go thread pinning.
The Windows name includes database volume/file identity. Linux local execution uses a nonblocking file lock per configured database path and Task ID; its lock files are retained to avoid changing a held lock inode. Database hardlink aliases are outside the v0 acceptance requirements.
See [CreateMutexW](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-createmutexw)
and [file identity](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getfileinformationbyhandle).

The gate spans cancellation and final commit but never holds a SQLite write
transaction while waiting. Existing execution capture/settlement remains free to
finish. A competing worker completion Claim is recorded as `BLOCKED` independently
of its Attempt's settlement. Ordinary informational Claims remain available.
Before suspension commits, Core rechecks Attempts, all leases (including protected
tests) and the execution slot; shared state validation enforces the same condition
on both SUSPENDED and CLOSED Tasks.

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
in both distros, runs only the release selection with `-tags=release`, and records
the source HEAD, binary hash, test log and mapping. Neither script overwrites the
accepted root `scp.exe`. O5 review and a user's explicit replacement follow release
verification; a green daily suite never replaces the controller automatically.

Pass `none` only after reviewing normative conformance as well as tests; otherwise
pass the remaining failures. Unsupported release hosts fail rather than skip or
substitute mocks. Daily Linux evidence is recorded separately and must not be
inferred from the Windows release result.
