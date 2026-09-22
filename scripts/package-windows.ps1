param([Parameter(Mandatory = $true)][string]$Compiler)
$ErrorActionPreference = 'Stop'
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $version = (Get-Content -LiteralPath 'cmd/scp/VERSION' -Raw).Trim()
    if ($version -notmatch '^\d+\.\d+\.\d+$') { throw 'Invalid release version' }
    $head = (git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Source HEAD unavailable' }
    $changes = @(git status --porcelain --untracked-files=all)
    if ($LASTEXITCODE -ne 0 -or $changes.Count) { throw 'Commit the source before packaging.' }
    $evidence = Join-Path (Get-Location) ".local\acceptance\$head"
    $binary = Join-Path $evidence 'scp.exe'
    $report = Get-Content -LiteralPath (Join-Path $evidence 'report.md') -Raw
    foreach ($line in @("HEAD: $head", 'Release suite exit: 0', 'Release vet exit: 0', 'Skipped tests: 0', 'Missing cases: ', 'Known normative failures: none')) {
        if (($report -split '\r?\n') -cnotcontains $line) { throw "Release evidence missing: $line" }
    }
    $digest = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
    $recorded = ((Get-Content -LiteralPath (Join-Path $evidence 'scp.exe.sha256') -Raw).Trim() -split '\s+')[0]
    if ($digest -cne $recorded) { throw 'Accepted binary hash mismatch' }
    $reportedVersion = & $binary --version
    if ($LASTEXITCODE -ne 0 -or $reportedVersion -cne "SCP Harness v$version") { throw 'Accepted binary version mismatch' }
    $output = Join-Path (Get-Location) ".local\releases\v$version"
    New-Item -ItemType Directory -Path $output -Force | Out-Null
    & $Compiler "/DAppVersion=$version" "/DReleaseDir=$evidence" "/DOutputDir=$output" 'scripts/windows-installer.iss'
    if ($LASTEXITCODE -ne 0) { throw 'Installer compilation failed' }
    $installer = Join-Path $output "SCP-Harness-$version-windows-x64-setup.exe"
    $installerDigest = (Get-FileHash -LiteralPath $installer -Algorithm SHA256).Hash.ToLowerInvariant()
    "$installerDigest  $(Split-Path -Leaf $installer)" | Set-Content -LiteralPath ($installer + '.sha256') -Encoding ascii
    [ordered]@{version=$version; source_commit=$head; binary_sha256=$digest; installer_sha256=$installerDigest} |
        ConvertTo-Json | Set-Content -LiteralPath (Join-Path $output 'release.json') -Encoding utf8
    Write-Output "Installer: $installer"
} finally { Pop-Location }
