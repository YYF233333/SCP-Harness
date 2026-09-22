param([Parameter(Mandatory = $true)][string]$KnownNormativeFailures)
$ErrorActionPreference = 'Stop'
$previousAcceptanceExe = $env:SCP_ACCEPTANCE_EXE
$previousCodexEvidence = $env:SCP_CODEX_EVIDENCE
function Test-DirtySource {
    # Existing untracked report bundles are not executable or normative inputs.
    # Tracked edits and every other untracked file still fail source admission.
    $changes = @(git status --porcelain --untracked-files=all)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect source status' }
    return @($changes | Where-Object { $_ -notmatch '^\?\? docs/reports/(.*\.(md|zip|sha256)|.*/\.gitattributes)$' }).Count -ne 0
}
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $sourceHead = (git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $sourceHead -notmatch '^[0-9a-f]{40}$') { throw 'Source HEAD unavailable' }
    if (Test-DirtySource) { throw 'Commit the reviewed source before release acceptance; working tree must be clean.' }
    if (Get-Process -Name scp -ErrorAction SilentlyContinue) { throw 'Stop the normal scheduler before Windows release acceptance.' }
    $osInfo = Get-CimInstance Win32_OperatingSystem
    if ([int]$osInfo.BuildNumber -lt 22000 -or $osInfo.Caption -notmatch 'Windows 11') { throw 'Windows 11 target host is required' }
    $env:WSL_UTF8 = '1'
    $distros = (wsl --list --verbose | Out-String) -replace "`0", ''
    if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate WSL distros' }
    foreach ($distro in @('SCP-Worker', 'SCP-Test')) {
        if ($distros -notmatch ([regex]::Escape($distro) + '\s+\S+\s+2')) { throw "Dedicated $distro WSL2 is required" }
    }
    $wslConfig = Get-Content -LiteralPath (Join-Path $env:USERPROFILE '.wslconfig') -Raw
    if ($wslConfig -notmatch '(?ims)^\s*\[wsl2\]\s*$[^\[]*?^\s*memory\s*=\s*8GB\s*$') { throw 'Configure the shared WSL2 VM memory=8GB limit first.' }
    wsl -d SCP-Test -u scp --cd / --exec python3 -c 'm=int(next(x.split()[1] for x in open("/proc/meminfo") if x.startswith("MemTotal:"))); assert 0 < m <= 8*1024*1024, m'
    if ($LASTEXITCODE -ne 0) { throw 'The 8 GiB WSL2 VM limit is not active; restart WSL after configuration.' }
    $evidence = Join-Path (Get-Location) ".local\acceptance\$sourceHead"
    New-Item -ItemType Directory -Path $evidence -Force | Out-Null
    & '.\scripts\install-test-workers.ps1'
    if ($LASTEXITCODE -ne 0) { throw 'Real fixture installation failed' }
    $acceptanceExe = Join-Path $evidence 'scp.exe'
    go build -trimpath -buildvcs=true -o $acceptanceExe ./cmd/scp
    if ($LASTEXITCODE -ne 0) { throw 'Source rebuild failed' }
    $env:SCP_ACCEPTANCE_EXE = $acceptanceExe
    $env:SCP_CODEX_EVIDENCE = Join-Path $evidence 'codex'
    $cases = [ordered]@{
        CLI = @('TestAcceptanceExecutable')
        Control = @('TestR1bCrossProcessControlAndSettlement', 'TestR1bTaskControlIdentityAndEntrypoints')
        Recovery = @('TestR1bControlExitRequiresExplicitRecovery', 'TestActualCoreCrashCapturesWorkerAndChargesFullLease')
        Scheduler = @('TestRunningSchedulerSuspendSerializationSignalAndStaleLock')
        Walkthrough = @('TestFrozenVortonA10')
        Workers = @('TestRealWorkerLifecycle')
        Isolation = @('TestActualInteropAndAutomountIsolation', 'TestWorkerRuntimeLifetime', 'TestWorkerHostAuthorityDenied', 'TestWorkerRuntimeAdmission')
        HumanRelease = @('TestHumanOptionReleaseIntegration')
        Observation = @('TestLiveAttemptObservation', 'TestObservationInterruptedLogs', 'TestObservationTimedOutLogs', 'TestObservationReadonlyRejection', 'TestObservationConcurrentCapture', 'TestObservationContinuationBase', 'TestObservationCLIGolden')
        Regressions = @('TestR1ClaimsPreserveLifecycleCancellation', 'TestR3OversizedWorkspaceCleanupProgress', 'TestR3ProtectedCopyReallyDiscarded', 'TestR4UnavailableSnapshotHasNoCandidateEffects', 'TestR4ExactSnapshotAttributes', 'TestProtectedTestCannotBeOverriddenAndReviewerReadonly')
        CodexDiscussionRelease = @('TestCodexExecutionBoundary')
    }
    $required = @($cases.Values | ForEach-Object { $_ })
    $selection = '^(' + (($required | ForEach-Object { [regex]::Escape($_) }) -join '|') + ')$'
    $testLog = Join-Path $evidence 'release-windows.jsonl'
    go test -p=1 -tags 'release codex_integration' ./... -count=1 -json -timeout=45m -run $selection | Set-Content -LiteralPath $testLog -Encoding utf8
    $testExit = $LASTEXITCODE
    $events = @(Get-Content -LiteralPath $testLog | ForEach-Object { $_ | ConvertFrom-Json })
    $failed = @($events | Where-Object { $_.Action -eq 'fail' })
    $skipped = @($events | Where-Object { $_.Action -eq 'skip' -and $_.Test })
    $passed = @{}
    foreach ($event in $events) { if ($event.Action -eq 'pass' -and $event.Test) { $passed[$event.Test] = $true } }
    $missing = @($required | Where-Object { -not $passed.ContainsKey($_) })
    go vet -tags 'release codex_integration' ./... 2>&1 | Set-Content -LiteralPath (Join-Path $evidence 'go-vet.log') -Encoding utf8
    $vetExit = $LASTEXITCODE
    $binaryHash = (Get-FileHash -LiteralPath $acceptanceExe -Algorithm SHA256).Hash.ToLowerInvariant()
    "$binaryHash  scp.exe" | Set-Content -LiteralPath (Join-Path $evidence 'scp.exe.sha256') -Encoding utf8
    $passedAll = $testExit -eq 0 -and $vetExit -eq 0 -and $failed.Count -eq 0 -and $skipped.Count -eq 0 -and $missing.Count -eq 0 -and $KnownNormativeFailures -ceq 'none'
    $report = @(
        '# Windows release verification — O5 review pending', '',
        "HEAD: $sourceHead", "UTC: $([DateTime]::UtcNow.ToString('o'))",
        "Host: $($osInfo.Caption), build $($osInfo.BuildNumber)",
        "Build: go build -trimpath -buildvcs=true -o $acceptanceExe ./cmd/scp",
        "scp.exe SHA-256: $binaryHash", "Release suite exit: $testExit", "Release vet exit: $vetExit",
        "Skipped tests: $($skipped.Count)", "Missing cases: $($missing -join ', ')",
        "Known normative failures: $KnownNormativeFailures", '',
        'This suite validates actual Windows/WSL boundaries. Daily Linux semantics are verified separately with go test ./... and go vet ./....',
        'The accepted controller is unchanged. O5 review is required before a user replaces it with this release candidate.', '',
        '| Windows release boundary | Executed evidence |', '| --- | --- |'
    )
    foreach ($case in $cases.Keys) { $report += "| $case | $($cases[$case] -join ', ') |" }
    $report | Set-Content -LiteralPath (Join-Path $evidence 'report.md') -Encoding utf8
    if (-not $passedAll) { throw "Windows release verification failed. Evidence: $evidence" }
    if ((git rev-parse HEAD).Trim() -ne $sourceHead -or (Test-DirtySource)) { throw 'Source changed during release acceptance' }
    Write-Output "Windows release suite passed; O5 review pending: $evidence"
} finally { $env:SCP_ACCEPTANCE_EXE = $previousAcceptanceExe; $env:SCP_CODEX_EVIDENCE = $previousCodexEvidence; Pop-Location }
