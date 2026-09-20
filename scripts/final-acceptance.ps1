$ErrorActionPreference = 'Stop'
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
    $evidence = Join-Path (Get-Location) '.local\acceptance'
    New-Item -ItemType Directory -Path $evidence -Force | Out-Null
    & '.\scripts\install-test-workers.ps1'
    if ($LASTEXITCODE -ne 0) { throw 'Real fixture executables could not be installed' }
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
    }
    $missing = @()
    foreach ($name in @('TestArchitectureConstraints','TestFrozenSchemaCapabilityAndContextRegistries','TestRoleCardMathematicalNumbersMatchJSONSchema','TestInfluenceCannotChangeSchedulingAuthorizationOrLease','TestStableErrorExitCodes')) { if (-not $passed.ContainsKey($name)) { $missing += $name } }
    foreach ($case in $cases.Keys) { foreach ($name in $cases[$case]) { if (-not $passed.ContainsKey($name)) { $missing += "$case/$name" } } }
    if ($testExit -ne 0 -or $failures.Count -gt 0 -or $testSkips.Count -gt 0 -or $missing.Count -gt 0) {
        @('FINAL ACCEPTANCE: FAIL', "HEAD: $sourceHead", "go test exit: $testExit", "Failed events: $($failures.Count)", "Skipped tests: $($testSkips.Count)", "Missing cases: $($missing -join ', ')", "Evidence: $testLog") | Set-Content -LiteralPath (Join-Path $evidence 'report.md') -Encoding utf8
        throw 'Final acceptance failed. Inspect go-test.jsonl; no completion claim is permitted.'
    }
    go vet ./... 2>&1 | Set-Content -LiteralPath (Join-Path $evidence 'go-vet.log') -Encoding utf8
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    $buildCommand = 'go build -trimpath -buildvcs=true -o scp.exe ./cmd/scp'
    go build -trimpath -buildvcs=true -o scp.exe ./cmd/scp
    if ($LASTEXITCODE -ne 0) { throw 'Source rebuild failed' }
    if ((git rev-parse HEAD).Trim() -ne $sourceHead -or (git status --porcelain)) { throw 'Source changed during acceptance' }
    $binaryHash = (Get-FileHash -Algorithm SHA256 -LiteralPath '.\scp.exe').Hash.ToLowerInvariant()
    $report = @(
        '# SCP Harness v0 Final Acceptance',
        '',
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
        'Known normative failures: none',
        '',
        'Package-level "no test files" events are not skipped acceptance tests; their production code is exercised by the cross-package unit and integration cases below.',
        '',
        '| Acceptance | Result | Executed evidence |',
        '| --- | --- | --- |'
    )
    foreach ($case in $cases.Keys) { $report += "| $case | PASS | $($cases[$case] -join ', ') |" }
    $report += @('', 'The full machine-readable execution log is go-test.jsonl alongside this report. Git history fixtures use 1/100/10000 commits and 200 branches. Worker fixtures are ordinary executables built from delivered Go source; Core has no test-only execution path. Fault tests use real process exits/kills, WSL termination, SQLite failure injection, missing executables and filesystem failures.', '', 'SCP Harness v0 implementation complete')
    $report | Set-Content -LiteralPath (Join-Path $evidence 'report.md') -Encoding utf8
    Write-Output "Final acceptance evidence: $evidence"
    Write-Output "HEAD: $sourceHead"
    Write-Output "scp.exe SHA-256: $binaryHash"
} finally { Pop-Location }
