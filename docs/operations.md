# SCP Harness v0 operations

Source and final acceptance are governed by `docs/spec/scp_harness_v0_execution_plan.md`.
All Windows development and commands below run in the original repository.

## Toolchain and dedicated worker environment

Install Windows tools through Scoop:

```powershell
scoop install go git python311
go version
git --version
```

The validated build toolchain is Go 1.27.1 windows/amd64. `modernc.org/sqlite`
is pinned in `go.mod`; `go.sum` pins its transitive module graph. No additional
direct Go dependency is used. WSL2 is a Windows OS prerequisite.

Provision a new dedicated distro from an official Ubuntu rootfs; never use or
clone an existing development distro. The bootstrap used this explicit import:

```powershell
# Download from https://releases.ubuntu.com/noble/ and verify SHA256SUMS first.
# ubuntu-24.04.4-wsl-amd64.wsl SHA-256:
# 9b2f7730dc68227dd04a9f3e5eab86ad85caf556b8606ad94f1f29ff5c4fd3f5
wsl --import SCP-Worker "$env:LOCALAPPDATA\SCP-Harness\SCP-Worker" <verified-rootfs-path> --version 2
.\scripts\setup-worker-wsl.ps1
```

The setup script only configures `SCP-Worker`. It disables drive automount and
Windows interop, disables systemd, creates `scp`, `/scp` and `/opt/scp-workers`,
and verifies isolation. Linux `python3`, `tar` and `git` are required; the documented
Ubuntu image contains them. Install each worker's own runtime/credentials inside
this distro. Authoritative repositories, SQLite and Artifact storage stay on Windows.
The trust boundary assumes configured native worker software is trusted; model
outputs and candidate code do not gain Core authority.

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
test command is `go test ./...`; install that runtime inside the distro if used.
These are examples, not defaults. `scp init` also requires a valid existing config.

```powershell
.\scripts\build.ps1
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
non-waiting Core control gate per physical database file and Task ID. Contention
returns `BLOCKED`; it is not retried. The Windows gate uses atomic creation of a
named kernel mutex and retains only the first creator's non-inheritable handle;
handle lifetime provides exclusion without thread ownership or Go thread pinning.
The name includes database volume/file identity, so path aliases share the gate.
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
same global slot, terminates WSL, captures writable interrupted state, charges
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

## Build, test and audit

```powershell
.\scripts\install-test-workers.ps1
go test ./...
go vet ./...
.\scripts\build.ps1
.\scripts\final-acceptance.ps1 -KnownNormativeFailures none
```

The fixture executable is compiled from `testdata/workers/main.go` and installed
in the dedicated distro. It is an ordinary configured executable, never linked
into production Core. Integration tests use real Windows Git, SQLite, WSL,
process termination, filesystem permissions and CAS. They fail on unsupported
hosts; no WSL acceptance test silently skips. Do not run a production scheduler
while these tests use the single dedicated worker distro.

Pass `none` only after reviewing normative conformance as well as tests; otherwise
pass the remaining failures. The script records this explicit assessment and does
not infer it from a green test run. It builds and tests the delivered executable,
records the exact HEAD, build command, binary SHA-256, uncached test results and
acceptance mapping under `.local/acceptance/<HEAD>/`, and submits for O5 review.
`scp.exe` and this generated evidence are derived outputs. All production code,
embedded helper/SQL sources, tests and scripts belong to the source commit.
