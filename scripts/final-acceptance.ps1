param([Parameter(Mandatory = $true)][string]$KnownNormativeFailures)
$ErrorActionPreference = 'Stop'
$previousAcceptanceExe = $env:SCP_ACCEPTANCE_EXE
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $sourceHead = (git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $sourceHead -notmatch '^[0-9a-f]{40}$') { throw 'Source HEAD unavailable' }
    if (git status --porcelain) { throw 'Commit the reviewed source before final acceptance; working tree must be clean.' }
    $osInfo = Get-CimInstance Win32_OperatingSystem
    if ([int]$osInfo.BuildNumber -lt 22000 -or $osInfo.Caption -notmatch 'Windows 11') { throw 'Windows 11 target host is required' }
    $env:WSL_UTF8 = '1'
    $distros = (wsl --list --verbose | Out-String) -replace "`0", ''
    if ($LASTEXITCODE -ne 0 -or $distros -notmatch 'SCP-Worker\s+\S+\s+2') { throw 'Dedicated SCP-Worker WSL2 is required' }
    $evidence = Join-Path (Get-Location) ".local\acceptance\$sourceHead"
    New-Item -ItemType Directory -Path $evidence -Force | Out-Null
    & '.\scripts\install-test-workers.ps1'
    if ($LASTEXITCODE -ne 0) { throw 'Real fixture executables could not be installed' }
    $acceptanceExe = Join-Path $evidence 'scp.exe'
    $buildCommand = "go build -trimpath -buildvcs=true -o `"$acceptanceExe`" ./cmd/scp"
    go build -trimpath -buildvcs=true -o $acceptanceExe ./cmd/scp
    if ($LASTEXITCODE -ne 0) { throw 'Source rebuild failed' }
    $env:SCP_ACCEPTANCE_EXE = $acceptanceExe
    $testLog = Join-Path $evidence 'go-test.jsonl'
    go test ./... -count=1 -json | Set-Content -LiteralPath $testLog -Encoding utf8
    $testExit = $LASTEXITCODE
    $events = Get-Content -LiteralPath $testLog | ForEach-Object { $_ | ConvertFrom-Json }
    $failures = @($events | Where-Object { $_.Action -eq 'fail' })
    $testSkips = @($events | Where-Object { $_.Action -eq 'skip' -and $_.Test })
    $passed = @{}
    foreach ($event in $events) { if ($event.Action -eq 'pass' -and $event.Test) { $passed[$event.Test] = $true } }
    $cases = [ordered]@{
        A0 = @('Test10000CommitSyntheticHistoryAndActualInteropIsolation', 'TestRunningSchedulerSuspendSerializationSignalAndStaleLock', 'TestHistoryIndependentBoundedGitAndCAS')
        A1 = @('TestCLIJSONFrozenProjectionsAndRestart', 'TestStrictSchema', 'TestConfigRejectsUnknownAndMissingBounds')
        A2 = @('TestRealWorkerLifecycle', 'TestResultSchemas', 'TestResourceProposalExactAuthorizationAndNoLedgerEffect', 'TestWorkerProposalDenialDoesNotDiscardIndependentClaim')
        A3 = @('TestCaptureBoundsAtomicityAndIntegrity', 'TestActualCoreCrashCapturesWorkerAndChargesFullLease', 'TestExecutableModeSurvivesCaptureTestReviewAndPromotion')
        A4 = @('Test10000CommitSyntheticHistoryAndActualInteropIsolation', 'TestHistoryIndependentBoundedGitAndCAS')
        A5 = @('TestRandomResourceOperationsConserve', 'TestImmutableObjectsAndExplicitMergeLCA', 'TestFulfilledRetainsExistingCompletionSemantics')
        A6 = @('TestRunningSchedulerSuspendSerializationSignalAndStaleLock', 'TestBlockedPendingTestResumesAndResourcePauseKeepsTarget')
        A7 = @('TestProtectedTestCannotBeOverriddenAndReviewerReadonly', 'TestInvalidReviewReworksSameImmutableArtifact', 'TestMissingAndCrashedReviewAreSyntheticRejects', 'TestFrozenVortonA10')
        A8 = @('TestPartitionClosedSet', 'TestUnauthorizedReviewAndOneTimeIndependentExploration', 'TestFrozenVortonA10')
        A9 = @('TestPromotionJournalActualProcessCrashAndRecovery', 'TestActualCoreCrashCapturesWorkerAndChargesFullLease', 'TestRunningSchedulerSuspendSerializationSignalAndStaleLock', 'TestPersistentInfrastructureAndFailStop', 'TestRepositoryDriftEndsChainWithoutBlocker')
        A10 = @('TestFrozenVortonA10')
        R1 = @('TestR1ClaimsPreserveLifecycleCancellation')
        R2 = @('TestR2LaunchFailureRetainsArtifactStep')
        R3 = @('TestR3OversizedWorkspaceCleanupProgress', 'TestR3ProtectedCopyReallyDiscarded', 'TestR3ProtectedCleanupFailureCannotPass', 'TestR3HostTransientCleanupProgress')
        R4 = @('TestR4ExactSnapshotAttributes', 'TestR4ExactSHAIgnoresWorktreeAndIndex', 'TestR4NonTreeAttributeIsolation', 'TestR4InspectionFailsClosed', 'TestR4UnavailableSnapshotHasNoCandidateEffects')
    }
    $missing = @()
    foreach ($name in @('TestArchitectureConstraints','TestFrozenSchemaCapabilityAndContextRegistries','TestRoleCardMathematicalNumbersMatchJSONSchema','TestInfluenceCannotChangeSchedulingAuthorizationOrLease','TestStableErrorExitCodes','TestAcceptanceExecutable')) { if (-not $passed.ContainsKey($name)) { $missing += $name } }
    foreach ($case in $cases.Keys) { foreach ($name in $cases[$case]) { if (-not $passed.ContainsKey($name)) { $missing += "$case/$name" } } }
    [System.IO.File]::WriteAllText((Join-Path $evidence 'go-vet.log'), '')
    go vet ./... 2>&1 | Set-Content -LiteralPath (Join-Path $evidence 'go-vet.log') -Encoding utf8
    $vetExit = $LASTEXITCODE
    $binaryHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $acceptanceExe).Hash.ToLowerInvariant()
    "$binaryHash  scp.exe" | Set-Content -LiteralPath (Join-Path $evidence 'scp.exe.sha256') -Encoding utf8
    if ($testExit -ne 0 -or $vetExit -ne 0 -or $failures.Count -gt 0 -or $testSkips.Count -gt 0 -or $missing.Count -gt 0 -or $KnownNormativeFailures -cne 'none') {
        @('FINAL ACCEPTANCE SUBMISSION: FAIL', "HEAD: $sourceHead", "Build command: $buildCommand", "scp.exe SHA-256: $binaryHash", "go test exit: $testExit", "go vet exit: $vetExit", "Failed events: $($failures.Count)", "Skipped tests: $($testSkips.Count)", "Missing cases: $($missing -join ', ')", "Known normative failures: $KnownNormativeFailures", "Evidence: $testLog") | Set-Content -LiteralPath (Join-Path $evidence 'report.md') -Encoding utf8
        throw 'Final acceptance failed. Inspect evidence; no completion claim is permitted.'
    }
    if ((git rev-parse HEAD).Trim() -ne $sourceHead -or (git status --porcelain)) { throw 'Source changed during acceptance' }
    Copy-Item -LiteralPath $acceptanceExe -Destination '.\scp.exe' -Force
    $report = @(
        '# SCP Harness v0 corrective evidence — O5 review pending',
        '',
        'Overall status: REJECT pending O5 R4 independent review. This report is not FINAL ACCEPTANCE.',
        "HEAD: $sourceHead",
        "Build command: $buildCommand",
        "scp.exe SHA-256: $binaryHash",
        "UTC: $([DateTime]::UtcNow.ToString('o'))",
        "Host: $($osInfo.Caption), build $($osInfo.BuildNumber)",
        "Toolchain: $(go version)",
        'Source rebuild/auditability: PASS',
        'go test ./...: PASS (uncached, -count=1 -json)',
        'go vet ./...: PASS',
        'Architecture constraints: PASS',
        'Windows+WSL2 integration: PASS',
        'Role-card schema/golden CLI projections: PASS',
        'Scheduler transition table: PASS',
        'Frozen walkthrough: PASS (CP0-CP9)',
        'Skipped acceptance tests: 0',
        "Known normative failures: $KnownNormativeFailures (explicit implementer review, not inferred from test counts)",
        '',
        'Package-level "no test files" events are not skipped acceptance tests; their production code is exercised by the cross-package unit and integration cases below.',
        '',
        '| Acceptance | Result | Executed evidence |',
        '| --- | --- | --- |'
    )
    foreach ($case in $cases.Keys) { $report += "| $case | PASS | $($cases[$case] -join ', ') |" }
    $report += @('', 'R1: internal/core/core.go protects lifecycle cancellation from non-qualifying Claims; cancellation_test.go fixes the transaction interleaving explicitly.', 'R2: internal/worker/execute.go and internal/scheduler/scheduler.go retain the infrastructure outcome and original pending Artifact step; launch_failure_test.go uses a real Windows launch error after a successful probe.', 'R3: internal/wsl/files.py, internal/wsl/wsl.go and internal/artifact/discard.go perform bounded deletion with progress independently of content admission limits; scheduler cleanup_test.go also injects a real immutable-file deletion failure.', 'R4: internal/gitrepo/attributes.go uses one exact-SHA textual Git grep and a private, controlled archive environment. No attribute evaluation/parser is used. attributes_test.go checks conservative rejection, environment isolation, bounds and the unchanged round-trip. Budget: export_attr_inspection <= 1, export_tree <= 1; all prior budgets remain unchanged.', '', 'The full machine-readable execution log is go-test.jsonl alongside this report. The scp.exe in this directory was built before verification and executed by TestAcceptanceExecutable. Git history fixtures use 1/100/10000 commits and 200 branches. Worker fixtures are ordinary executables built from delivered Go source; Core has no test-only execution path. Fault tests use real process exits/kills, WSL termination, SQLite failure injection, missing executables and filesystem failures.', '', 'Implementation submitted for O5 independent review. Development stops here.')
    $report | Set-Content -LiteralPath (Join-Path $evidence 'report.md') -Encoding utf8
    Write-Output "Corrective verification evidence (O5 review pending): $evidence"
    Write-Output "HEAD: $sourceHead"
    Write-Output "scp.exe SHA-256: $binaryHash"
} finally { $env:SCP_ACCEPTANCE_EXE = $previousAcceptanceExe; Pop-Location }
