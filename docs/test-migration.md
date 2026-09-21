# O5 测试跨平台化与双 WSL 实现报告

状态：本工作包的日常 Linux 验证已通过，提交 O5 审核。双 WSL、Go 和统一 8 GiB 内存上限已配置；51 个 daily-linux 顶层测试全部通过，0 失败、0 跳过，go vet 退出码为 0。Windows release 验收尚未执行，当前控制器未替换。

审核范围：测试分层与删除、最小 Linux/双 WSL 执行支撑、配置及验证脚本、操作文档。基线提交：420737d6370bb7b40ef43c355ce0366743072cf5。

## Test migration

默认 Linux 层共 51 个顶层测试；Windows release 层共 9 个顶层测试。
默认 Linux 生产文件选择中没有 wsl.exe。Windows release 脚本只执行下表的 release-windows 项。
TestRecoveryProcessHelper 是真实崩溃子进程入口，随共享测试编译；Windows release 的恢复测试也会调用该入口。

| 顶层测试 | 分层 | 源文件 |
| --- | --- | --- |
| TestCLIJSONFrozenProjectionsAndRestart | daily-linux | cmd/scp/main_test.go |
| TestStableErrorExitCodes | daily-linux | cmd/scp/main_test.go |
| TestLocalExecutable | daily-linux | cmd/scp/suite_linux_test.go |
| TestLocalCrossProcessControlAndSettlement | daily-linux | cmd/scp/suite_linux_test.go |
| TestAcceptanceExecutable | release-windows | cmd/scp/suite_windows_test.go |
| TestR1bCrossProcessControlAndSettlement | release-windows | cmd/scp/suite_windows_test.go |
| TestCaptureBoundsAtomicityAndIntegrity | daily-linux | internal/artifact/artifact_test.go |
| TestR3HostTransientCleanupProgress | daily-linux | internal/artifact/discard_test.go |
| TestStrictSchema | daily-linux | internal/config/config_test.go |
| TestConfigRejectsUnknownAndMissingBounds | daily-linux | internal/config/config_test.go |
| TestRoleCardMathematicalNumbersMatchJSONSchema | daily-linux | internal/config/config_test.go |
| TestInfluenceCannotChangeSchedulingAuthorizationOrLease | daily-linux | internal/core/authority_test.go |
| TestFrozenSchemaCapabilityAndContextRegistries | daily-linux | internal/core/authority_test.go |
| TestR1ClaimsPreserveLifecycleCancellation | daily-linux | internal/core/cancellation_test.go |
| TestResourceProposalExactAuthorizationAndNoLedgerEffect | daily-linux | internal/core/claims_test.go |
| TestWorkerProposalDenialDoesNotDiscardIndependentClaim | daily-linux | internal/core/claims_test.go |
| TestFulfilledRetainsExistingCompletionSemantics | daily-linux | internal/core/claims_test.go |
| TestR1bCancellationCannotBeTakenOver | daily-linux | internal/core/control_test.go |
| TestR1bControlInterleavings | daily-linux | internal/core/control_test.go |
| TestR1bCancellationErrorKeepsProtection | daily-linux | internal/core/control_test.go |
| TestR1bWorkerCompletionDoesNotBlockSettlement | daily-linux | internal/core/control_test.go |
| TestR1bInactiveTaskInvariant | daily-linux | internal/core/control_test.go |
| TestRandomResourceOperationsConserve | daily-linux | internal/core/ledger_test.go |
| TestImmutableObjectsAndExplicitMergeLCA | daily-linux | internal/core/ledger_test.go |
| TestTaskControlEntrypoints | daily-linux | internal/core/suite_linux_test.go |
| TestR1bTaskControlIdentityAndEntrypoints | release-windows | internal/core/suite_windows_test.go |
| TestArchitectureConstraints | daily-linux | internal/gitrepo/architecture_test.go |
| TestR4ExactSnapshotAttributes | daily-linux | internal/gitrepo/attributes_test.go |
| TestR4ExactSHAIgnoresWorktreeAndIndex | daily-linux | internal/gitrepo/attributes_test.go |
| TestR4NonTreeAttributeIsolation | daily-linux | internal/gitrepo/attributes_test.go |
| TestR4InspectionFailsClosed | daily-linux | internal/gitrepo/attributes_test.go |
| TestHistoryIndependentBoundedGitAndCAS | daily-linux | internal/gitrepo/git_test.go |
| TestR4UnavailableSnapshotHasNoCandidateEffects | daily-linux | internal/scheduler/archive_attributes_test.go |
| TestR3OversizedWorkspaceCleanupProgress | daily-linux | internal/scheduler/cleanup_test.go |
| TestR3ProtectedCopyReallyDiscarded | daily-linux | internal/scheduler/cleanup_test.go |
| TestR1bProtectedAdmissionChecksCancellation | daily-linux | internal/scheduler/control_recovery_test.go |
| Test10000CommitSyntheticHistory | daily-linux | internal/scheduler/isolation_test.go |
| TestUnauthorizedReviewAndOneTimeIndependentExploration | daily-linux | internal/scheduler/isolation_test.go |
| TestExecutableModeSurvivesCaptureTestReviewAndPromotion | daily-linux | internal/scheduler/isolation_test.go |
| TestRecoveryProcessHelper | daily-linux | internal/scheduler/recovery_test.go |
| TestPromotionJournalActualProcessCrashAndRecovery | daily-linux | internal/scheduler/recovery_test.go |
| TestLocalVortonWalkthrough | daily-linux | internal/scheduler/suite_linux_test.go |
| TestLocalWorkerLifecycle | daily-linux | internal/scheduler/suite_linux_test.go |
| TestLocalSchedulerControl | daily-linux | internal/scheduler/suite_linux_test.go |
| TestLocalControlExitRequiresExplicitRecovery | daily-linux | internal/scheduler/suite_linux_test.go |
| TestLocalCoreCrashCapturesWorkerAndChargesFullLease | daily-linux | internal/scheduler/suite_linux_test.go |
| TestFrozenVortonA10 | release-windows | internal/scheduler/suite_windows_test.go |
| TestRealWorkerLifecycle | release-windows | internal/scheduler/suite_windows_test.go |
| TestRunningSchedulerSuspendSerializationSignalAndStaleLock | release-windows | internal/scheduler/suite_windows_test.go |
| TestR1bControlExitRequiresExplicitRecovery | release-windows | internal/scheduler/suite_windows_test.go |
| TestActualCoreCrashCapturesWorkerAndChargesFullLease | release-windows | internal/scheduler/suite_windows_test.go |
| TestActualInteropAndAutomountIsolation | release-windows | internal/scheduler/suite_windows_test.go |
| TestProtectedTestCannotBeOverriddenAndReviewerReadonly | daily-linux | internal/scheduler/transitions_test.go |
| TestInvalidReviewReworksSameImmutableArtifact | daily-linux | internal/scheduler/transitions_test.go |
| TestMissingAndCrashedReviewAreSyntheticRejects | daily-linux | internal/scheduler/transitions_test.go |
| TestBlockedPendingTestResumesAndResourcePauseKeepsTarget | daily-linux | internal/scheduler/transitions_test.go |
| TestRepositoryDriftEndsChainWithoutBlocker | daily-linux | internal/scheduler/transitions_test.go |
| TestPersistentInfrastructureAndFailStop | daily-linux | internal/scheduler/transitions_test.go |
| TestResultSchemas | daily-linux | internal/worker/protocol_test.go |
| TestPartitionClosedSet | daily-linux | internal/worker/protocol_test.go |
| TestR2LaunchFailureRetainsArtifactStep | deleted | internal/scheduler/launch_failure_test.go 已删除 |
| TestR3ProtectedCleanupFailureCannotPass | deleted | 从 internal/scheduler/cleanup_test.go 删除 |

TestR1bTaskControlIdentityAndEntrypoints 只删除 database hardlink alias 段，正常 Task/数据库作用域及控制入口互斥检查保留。continue-worker、FIFO、chattr、cleanup-fault 局部 helper 与无用 imports 已删除。没有用替代性极端故障测试补足数量。

## Split tests

共享测试函数保留原有语义断言。Linux 入口直接使用本地真实进程；Windows 入口带 windows && release 标签，使用真实 Win32 / WSL 边界。

| 原测试 | portable / daily-linux | Windows-specific / release-windows |
| --- | --- | --- |
| TestAcceptanceExecutable | TestLocalExecutable | TestAcceptanceExecutable |
| TestR1bCrossProcessControlAndSettlement | TestLocalCrossProcessControlAndSettlement | TestR1bCrossProcessControlAndSettlement |
| TestR1bTaskControlIdentityAndEntrypoints | TestTaskControlEntrypoints | TestR1bTaskControlIdentityAndEntrypoints |
| TestR1bControlExitRequiresExplicitRecovery | TestLocalControlExitRequiresExplicitRecovery | TestR1bControlExitRequiresExplicitRecovery |
| TestRunningSchedulerSuspendSerializationSignalAndStaleLock | TestLocalSchedulerControl | TestRunningSchedulerSuspendSerializationSignalAndStaleLock |
| TestFrozenVortonA10 | TestLocalVortonWalkthrough | TestFrozenVortonA10 |
| TestRealWorkerLifecycle | TestLocalWorkerLifecycle | TestRealWorkerLifecycle |
| Test10000CommitSyntheticHistoryAndActualInteropIsolation | Test10000CommitSyntheticHistory | TestActualInteropAndAutomountIsolation |
| TestActualCoreCrashCapturesWorkerAndChargesFullLease | TestLocalCoreCrashCapturesWorkerAndChargesFullLease | TestActualCoreCrashCapturesWorkerAndChargesFullLease |


历史长度、synthetic Git 内容隔离和调用预算移到 Test10000CommitSyntheticHistory。PE 执行、automount 与两个 distro 的独立文件系统检查属于 TestActualInteropAndAutomountIsolation。
CAS 前后真实进程退出与 promotion journal recovery 保留在默认 Linux 层，不以 mock 替代。R3 bounds、protected-copy Artifact 隔离和后续执行恢复保留；disposable copy 检查现在还验证它不会清掉 worker 环境。

## Infrastructure changes

- Local integration fixture：fixture_linux_test.go 在测试开始时编译普通 worker executable；当前非 root 用户直接执行，使用真实临时 workspace、Git、SQLite 和 Artifact；执行目录以数据库文件名区分。Linux subprocess 使用 process group；Core 崩溃后显式 recovery 根据执行记录终止遗留 group。捕获前等待 group 中仍可执行的进程退出。
- Windows integration fixture：fixture_windows_test.go 检查两个 dedicated distro；原 Win32 control gate 不变。Linux 本地 control gate 使用配置数据库旁的非阻塞 flock，保持 Task/database 作用域。
- Git platform helper：gitrepo.Executable() 选择 git.exe / git；null device 使用 os.DevNull。Linux repository identity 保留路径大小写。沿用同一套 Git export、attribute inspection、construct 与 CAS 实现。
- SCP-Test：新增 wsl.test_distro=SCP-Test；Core.TestRunner 专用于 protected test，独立恢复和清理。setup-test-wsl.ps1 提供经过 SHA-256 校验的官方 rootfs 导入及 Go/Git/Python/tar 配置。
- SCP-Worker：继续运行所有普通 worker Attempts；setup-worker-wsl.ps1 为两个 distro 配置相同的 automount/interop 禁令。install-test-workers.ps1 将 release fixture 安装到两个 distro。
- .wslconfig：configure-wsl-memory.ps1 备份用户原配置，保留其他设置，写入 [wsl2] memory=8GB 后重启 WSL；用户已执行此脚本；实测 SCP-Test 的 MemTotal 为 8129708 kB。
- 依赖预置：install-test-modules.ps1 校验 Windows 已有缓存，仅同步 go.mod 锁定的 11 个模块版本到 SCP-Test。Linux Go 继续校验 go.sum，TLS/checksum 设置不变；不依赖 Windows drive automount 或 interop。
- 日常入口：Linux 直接 go test ./...、go vet ./...，或 sh scripts/test-daily.sh。Windows 的 test-daily.ps1 仅传输源文件执行快照并在 SCP-Test 内启动上述 Linux 命令；Linux 测试内部不会再调用 wsl.exe。开发修改始终留在原始仓库。
- 验收入口：final-acceptance.ps1 的映射只含九个 Windows release 测试，不重复将全部普通语义测试作为 Windows-only 验收；R2/R3 已删除测试没有映射。
- 控制器：build.ps1 输出 .local/build/scp.exe；release 候选输出 .local/acceptance/<HEAD>/scp.exe。两者均不覆盖当前已验收控制器，替换需先完成真实 release 验证及 O5 review。

ResourceLedger、single global execution slot、Task/Option/promotion 语义未改变。没有新增 generic runner/sandbox/repository framework、Docker、mock 或 production test hook。WSL 内日常测试生成的是临时 fixture 数据，不导入生产 authoritative repository、Core SQLite 或 Artifact store。

## Validation

验证日期：2026-09-22（Asia/Tokyo）。用户在真实 SCP-Test WSL2 中执行日常入口，Agent 核对完整 JSON 日志、执行清单、退出码和源文件快照。

| 验证 | 结果 |
| --- | --- |
| daily-linux full test：go test ./... -count=1 -json -timeout=30m | PASS：51 个顶层测试，179 个含子测试的通过记录，0 失败、0 跳过 |
| SCP-Test 内 go vet ./... | PASS：退出码 0，日志为空 |
| 验收清单覆盖 | PASS：全部 51 个 daily-linux 测试均在日志中通过；release mapping 精确对应 9 个 Windows 入口 |
| 受测源码一致性 | PASS：提交前核对 85 个代码、脚本、配置等文件，与成功运行的执行快照一致；之后仅更新本报告 |
| Linux 全部测试交叉编译与 Linux 目标 go vet | PASS（运行前静态验证） |
| Windows go vet、release 测试编译及 go vet -tags=release | PASS（静态验证） |
| PowerShell / embedded Python 语法及 git diff --check | PASS |
| Linux build selection | PASS：选入的生产文件没有 wsl.exe |
| SCP-Test 工具链与隔离 | PASS：Go 1.27.1 linux/amd64、scp UID 1000、interop/automount 禁用 |
| WSL2 共享 VM 内存上限 | PASS：[wsl2] memory=8GB；SCP-Test MemTotal=8129708 kB |
| Windows release suite 真实执行 | 未执行，保留为独立 release / milestone 验收 |

Linux 七个含测试的 package 均通过；scheduler package 用时 48.281 秒。CLI、SQLite、Artifact、Git、protected test、process crash/recovery 使用真实机制。本次未运行或替代 Windows release 边界测试。

成功运行的本地证据目录：.local/daily/20260922-000410，包含 result.json、go-test.jsonl、go-test.stderr.log、go-vet.log、source.tar 和 verified-summary.json。该目录按仓库规则忽略，不随提交上传。

受测执行快照 source.tar 的 SHA-256：

```text
0d550e88febc12edf1de0f2dab7286ced5f3d0296d9a213e381adcd15323397d
```

执行快照保留在 SCP-Test 的 /tmp/scp-daily.0rD3YKn9，由用户手动清理。源码开发、提交和推送均在原始 Windows 仓库进行。

先前的受限沙箱运行曾因 Windows 路径访问拒绝失败；初次 Linux 运行曾因 Go module proxy TLS 超时未能编译 SQLite 相关包。依赖缓存预置后，本次真实 Linux 全量重跑已通过；早期失败未被计为通过或 Windows release 证据。

## 审核与发布边界

- 本报告提交日常 Linux 迁移和双环境配置的执行结果，等待 O5 审核；不声明 Windows release acceptance PASS。
- 现有 ResourceLedger、单一 execution slot、Task/Option/promotion 语义不变；未修改模型、数据库 schema、既有 Win32 gate 或依赖版本。
- 新配置须包含 wsl.test_distro=SCP-Test；生产控制器替换前仍需停止 scheduler，执行独立 Windows release suite，并完成 O5 review。
- 根目录现有 scp.exe 未重建或覆盖。当前 SHA-256 为 411ae8861ba91bd0ca93d36049ebb23df9fc808858fc9e5efb73e7ba7a7566f0。
- 未新增 generic runner/sandbox/repository framework、Docker、production test hook，也未以 mock 替代真实 Linux 机制。

复现和配置命令见 [operations.md](operations.md)。
