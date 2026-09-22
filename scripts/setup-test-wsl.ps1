param(
    [string]$Rootfs,
    [string]$RootfsSHA256,
    [string]$GoArchive,
    [string]$GoArchiveSHA256
)
$ErrorActionPreference = 'Stop'
$env:WSL_UTF8 = '1'
if (Get-Process -Name scp,scph -ErrorAction SilentlyContinue) { throw 'Stop the normal SCP scheduler before provisioning SCP-Test.' }
$listing = (wsl --list --verbose | Out-String) -replace "`0", ''
if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate WSL distros.' }
if ($listing -notmatch 'SCP-Test\s+\S+\s+2') {
    if (-not $Rootfs -or $RootfsSHA256 -notmatch '^[0-9a-fA-F]{64}$') { throw 'A verified official Ubuntu rootfs and its published SHA-256 are required to create SCP-Test.' }
    $Rootfs = (Resolve-Path -LiteralPath $Rootfs).ProviderPath
    if ((Get-FileHash -LiteralPath $Rootfs -Algorithm SHA256).Hash -ine $RootfsSHA256) { throw 'Rootfs hash mismatch' }
    $installPath = Join-Path $env:LOCALAPPDATA 'SCP-Harness\SCP-Test'
    New-Item -ItemType Directory -Path $installPath -Force | Out-Null
    wsl --import SCP-Test $installPath $Rootfs --version 2
    if ($LASTEXITCODE -ne 0) { throw 'SCP-Test import failed' }
}
Write-Output 'Checking SCP-Test prerequisites...'
wsl -d SCP-Test -u root --cd / --exec sh -c 'command -v git >/dev/null && command -v python3 >/dev/null && command -v tar >/dev/null && test -s /etc/ssl/certs/ca-certificates.crt'
if ($LASTEXITCODE -ne 0) {
    Write-Output 'Installing missing prerequisites (update timeout: 180s; install timeout: 300s)...'
    wsl -d SCP-Test -u root --cd / --exec sh -c 'timeout 180 apt-get -o Acquire::Retries=0 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 update && DEBIAN_FRONTEND=noninteractive timeout 300 apt-get -o Acquire::Retries=0 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 install -y git python3 tar ca-certificates'
    if ($LASTEXITCODE -ne 0) { throw 'SCP-Test prerequisite installation failed or timed out' }
} else {
    Write-Output 'Git, Python, tar and certificates already present; skipping apt.'
}
if ($GoArchive) {
    Write-Output 'Installing the verified local Go archive...'
    if ($GoArchiveSHA256 -notmatch '^[0-9a-fA-F]{64}$' -or (Get-FileHash -LiteralPath $GoArchive -Algorithm SHA256).Hash -ine $GoArchiveSHA256) { throw 'Go archive must match its official SHA-256' }
    $installGo = @'
import pathlib, subprocess, sys
with pathlib.Path(sys.argv[1]).open('rb') as archive:
    subprocess.run(['wsl.exe', '-d', 'SCP-Test', '-u', 'root', '--cd', '/', '--exec', 'sh', '-c', 'test ! -e /usr/local/go && tar -xz -C /usr/local && ln -s /usr/local/go/bin/go /usr/local/bin/go && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt'], stdin=archive, check=True, timeout=120)
'@
    python -c $installGo $GoArchive
    if ($LASTEXITCODE -ne 0) { throw 'Go installation failed; existing toolchains are not overwritten.' }
}
& (Join-Path $PSScriptRoot 'setup-worker-wsl.ps1') -Distro SCP-Test
$goVersion = wsl -d SCP-Test -u scp --cd / --exec go version
if ($LASTEXITCODE -ne 0 -or $goVersion -notmatch 'go(\d+)\.(\d+)' -or ([int]$Matches[1] -eq 1 -and [int]$Matches[2] -lt 26)) { throw 'SCP-Test needs Go 1.26+; supply a verified official Linux amd64 Go archive on first setup.' }
Write-Output $goVersion
wsl -d SCP-Test -u scp --cd / --exec sh -c 'git --version && python3 --version && tar --version'
if ($LASTEXITCODE -ne 0) { throw 'SCP-Test toolchain verification failed' }
Write-Output 'SCP-Test toolchain installed. Run configure-wsl-memory.ps1 before validation.'
