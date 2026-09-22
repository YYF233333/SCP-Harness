# SCP Harness

SCP Harness is a Go CLI for bounded agent work on a Git repository. Core owns
Task/Option state, wall-time budgets, immutable Artifacts and repository promotion
in SQLite. Workers run external executables through a strict JSON protocol.

v0.2 workflow: create a Task, investigate readonly source, discuss/refine Options, allocate a budget, and explicitly release a Change. Mutation produces immutable Artifacts; independent CI and readonly review lead to AWAIT_PROMOTION. Only a separate `change promote` authorizes repository CAS.

Changes can pause/resume from their current Artifact, retry CI/review, rework or explicitly abort. Every activity releases the single global execution slot; control requests can queue while development runs. Repeated CI timeouts block instead of looping.

Use `scph --help`, `change watch`, `ci watch`, `attempt watch/diff`, `status` and `config show` to inspect execution and evidence. Development and acceptance must use independent config/database/runtime directories; v0.2 must not modify the installed v0.1.0 controller or its production database.

## Repository map

| Location | Responsibility |
| --- | --- |
| `cmd/scp` | CLI commands, JSON projections and executable tests |
| `internal/core`, `model`, `ledger`, `store` | State transitions, authority, budget accounting and SQLite persistence |
| `internal/scheduler` | Single-activity scheduling, CI, explicit promotion and recovery |
| `internal/worker`, `wsl`, `boundedexec` | Worker protocol, platform execution and bounded processes |
| `internal/artifact`, `gitrepo`, `config` | Artifact capture, exact Git snapshots and strict configuration |
| `testdata` | Role cards and executable worker fixtures |
| `scripts` | Build, environment setup and verification entry points |

## Build and verify

For global Windows use, install the [Windows package](docs/windows-installation.md)
and run `scph --version`. `cmd/scp/VERSION` records the release version.

The production host is Windows 11 with separate `SCP-Worker` and `SCP-Test` WSL2
distros. Daily tests use real local processes in unprivileged Linux. See
[operations](docs/operations.md) for provisioning and configuration.

From the original Windows repository, with the scheduler stopped:

```powershell
.\scripts\build.ps1
.\scripts\test-daily.ps1
```

The build goes to `.local/build/scp.exe`. The daily script runs the current source
in `SCP-Test` and saves test/vet evidence under `.local/daily/`. In Linux, use
`sh scripts/test-daily.sh`. Windows release acceptance is a separate step using
`scripts/final-acceptance.ps1`; the accepted root `scp.exe` is replaced manually.

## Documentation

- [Operations](docs/operations.md): setup, commands, observation, recovery and release validation.
- [Worker protocol](docs/worker-protocol.md): inputs, context and result contracts.
- [Execution boundary](docs/execution-boundary.md): OS permissions and worker runtime lifetime.
- [Normative specification](docs/spec/scp_harness_v0_execution_plan.md): product rules and their amendments; later explicit rulings supersede earlier provisions.
- [Frozen walkthrough](docs/spec/scp_harness_v0_walkthrough.md) and [role-card schema](docs/spec/scp_harness_role_card_v0.schema.json): acceptance inputs retained with the specification package.

Local deployment state, binaries and validation logs live under ignored `.local/`.
Historical reports describe their recorded source revision, not current acceptance.
