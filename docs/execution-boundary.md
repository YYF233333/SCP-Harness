# Worker execution boundary

O5 decision, 2026-09-22: the agent receives the full OS authority of the
unprivileged `scp` account. Core treats its configured executable as opaque.
Provider-specific permissions, sandbox selection and network restrictions are
not part of SCP's security model.

## Dedicated OS runtime

`scripts/setup-worker-wsl.ps1 -Distro SCP-Worker` installs the root-owned boot
script `scripts/worker-runtime-start.sh`. Boot commands are asynchronous in WSL;
Core waits for the root-owned ready marker before checking isolation or admitting
a worker. It checks effective persistent write access, including owner chmod
authority, without recognizing any provider.

| Path / channel | Worker access | Lifetime / enforcement |
| --- | --- | --- |
| `/scp/attempt/input.json`, `context/` | Read | Root-owned; Core materialization and bounded cleanup |
| `workspace/` for mutation | Read/write | Core capture after successful termination; bounded cleanup before reuse |
| `workspace/` for review or discussion | Read only | Root-owned Unix modes; never mutation capture |
| `workspace/` for none operations | Absent | No placeholder workspace |
| `/scp/attempt/result.json` | Read/write | Explicit, precreated output file; Core-owned parent |
| `/home/scp`, `/tmp`, `/var/tmp` | Read/write | Per-boot tmpfs; destroyed by distro termination |
| `/dev/shm`, `/run/shm`, `/run/lock` | Read/write | Volatile OS mounts; tested across termination |
| `/mnt`, `/run/WSL`, `/var/crash` | Inaccessible | Root-owned, searchable only by root; no shared WSL storage or interop socket authority |
| Installation and explicit credential input | Read/execute as configured | Root-owned; no persistent mutable home/config/cache |

Other persistent regular files and directories must not be writable or owned by
`scp`, except the Core-managed Attempt tree. Admission fails closed on violations.
Old disk home/temp contents are hidden by the volatile mounts, with the underlying
home inaccessible. Provisioning does not recursively delete them.

Automount, fstab mounts, interop and Windows PATH import remain disabled.
The shared WSL kernel may still register a Windows binary handler; the worker
cannot access its interop socket. Tests exercise a real PE executable both with
the normal environment and a deliberately supplied socket path.

Internet access remains enabled. `SCP-Test` retains its separate role, toolchain,
module cache and daily Linux test workflow; the worker's volatile home policy
does not apply to it.

## Installed Codex worker

The standalone installation bundle is maintained under the ignored
`.local/codex-real-worker-v0/` work package, outside Core. All six operations use:

```text
codex exec --dangerously-bypass-approvals-and-sandbox
  --ephemeral --skip-git-repo-check --ignore-rules --ignore-user-config
  -c agents.enabled=false
  -c model_reasoning_effort=<binding>
  -c sqlite_home=<attempt-runtime>/state
  -c log_dir=<attempt-runtime>/log
  [-m <explicit-model-binding>] -o <attempt-runtime>/final-result -
```

`HOME`, `CODEX_HOME`, XDG state/config/data/cache/runtime paths, developer caches
and `TMPDIR` point inside `/tmp/scp-codex-<attempt-id>`. The wrapper copies only
the explicitly provisioned `/opt/scp-runtime/auth.json` credential input into
that fresh runtime. The persistent source is root-owned, group-readable, and
must be renewed by the operator when required. Credential contents are never
included in the work package or evidence. The model may read its runtime copy.

No operation selects a Codex sandbox or permission profile. Workspace mode only
selects the working directory. The wrapper relays final bytes without semantic
repair/retry; Core retains result validation. Mandatory distro termination
discards state even after crashes, timeouts, interrupts and detached children.

## Validation entry points

Run sequentially with the normal scheduler stopped:

```powershell
go test -tags 'release codex_integration' ./internal/scheduler -run '^TestCodexExecutionBoundary$' -count=1 -v -timeout=40m
go test -tags release ./internal/wsl -count=1 -v -timeout=10m
./scripts/test-daily.ps1
```

The model integration is opt-in and requires the deployed, authenticated worker.
It uses synthetic fixtures through real Core and both WSL distros. It does not
create a self-hosting Task or replace the accepted controller. The OS tests use
the existing installed PE probe from Windows release isolation validation.
Windows release acceptance remains a separate reviewed-source validation layer.

Discussion uses this same OS boundary. Its protocol and capabilities are described
in [worker protocol](worker-protocol.md#discussion-and-host-release); deployment
upgrades and release checks are in [operations](operations.md#enabling-discussion-in-an-existing-deployment).
