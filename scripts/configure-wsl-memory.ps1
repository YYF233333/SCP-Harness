param()
$ErrorActionPreference = 'Stop'
if (Get-Process -Name scp, scph -ErrorAction SilentlyContinue) { throw 'Stop the normal SCP scheduler before reconfiguring WSL.' }
$configPath = Join-Path $env:USERPROFILE '.wslconfig'
$lines = [System.Collections.Generic.List[string]]::new()
if (Test-Path -LiteralPath $configPath) {
    Copy-Item -LiteralPath $configPath -Destination ($configPath + '.scp-backup-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
    $lines.AddRange([string[]](Get-Content -LiteralPath $configPath))
}
$sectionStart = -1
$sectionEnd = $lines.Count
for ($i = 0; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match '^\s*\[wsl2\]\s*$') { $sectionStart = $i; continue }
    if ($sectionStart -ge 0 -and $lines[$i] -match '^\s*\[') { $sectionEnd = $i; break }
}
if ($sectionStart -lt 0) {
    $lines.Add('[wsl2]')
    $lines.Add('memory=8GB')
} else {
    $memoryLine = -1
    for ($i = $sectionStart + 1; $i -lt $sectionEnd; $i++) {
        if ($lines[$i] -match '^\s*memory\s*=') {
            if ($memoryLine -ge 0) { throw 'Multiple memory settings in [wsl2]; review .wslconfig before continuing.' }
            $memoryLine = $i
        }
    }
    if ($memoryLine -ge 0) { $lines[$memoryLine] = 'memory=8GB' } else { $lines.Insert($sectionEnd, 'memory=8GB') }
}
[IO.File]::WriteAllLines($configPath, $lines, [Text.UTF8Encoding]::new($false))
wsl --shutdown
if ($LASTEXITCODE -ne 0) { throw 'Memory setting saved, but WSL shutdown failed; restart WSL before validation.' }
Write-Output 'WSL2 VM memory=8GB configured; other .wslconfig settings preserved.'
