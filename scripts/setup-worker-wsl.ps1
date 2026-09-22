param([ValidateSet('SCP-Worker', 'SCP-Test')][string[]]$Distro = @('SCP-Worker', 'SCP-Test'), [string]$GoArchive, [string]$GoArchiveSHA256)
$ErrorActionPreference = 'Stop'
$env:WSL_UTF8 = '1'
if ($GoArchive) {
    if ($GoArchiveSHA256 -notmatch '^[0-9a-fA-F]{64}$' -or (Get-FileHash -LiteralPath $GoArchive -Algorithm SHA256).Hash -ine $GoArchiveSHA256) { throw 'Go archive hash does not match the supplied official SHA-256.' }
}
if (Get-Process -Name scp, scph -ErrorAction SilentlyContinue) { throw 'Stop the normal SCP scheduler before configuring execution distros.' }
$listing = (wsl --list --verbose | Out-String) -replace "`0", ''
if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate WSL distros.' }
foreach ($name in $Distro) {
    if ($listing -notmatch ([regex]::Escape($name) + '\s+\S+\s+2')) { throw "Dedicated WSL2 $name must already exist. See docs/operations.md." }
}

$script = @'
set -eu
id scp >/dev/null 2>&1 || useradd --create-home --shell /bin/sh scp
mkdir -p /scp /opt/scp-workers
chown root:root /scp /opt/scp-workers
chmod 755 /scp /opt/scp-workers
cat > /etc/wsl.conf <<'CONFIG'
[automount]
enabled=false
mountFsTab=false
[interop]
enabled=false
appendWindowsPath=false
[boot]
systemd=false
[user]
default=scp
CONFIG
'@
foreach ($name in $Distro) {
    $script -replace "`r", '' | wsl -d $name -u root --cd / --exec sh -c "sed 's/\r$//' | sh"
    if ($LASTEXITCODE -ne 0) { throw "Dedicated $name configuration failed" }
    if ($name -eq 'SCP-Worker') {
        Get-Content -Raw -LiteralPath (Join-Path $PSScriptRoot 'worker-runtime-start.sh') | wsl -d $name -u root --cd / --exec sh -c "sed 's/\r$//' > /opt/scp-workers/runtime-start && chown root:root /opt/scp-workers/runtime-start && chmod 755 /opt/scp-workers/runtime-start && sed -i '/^systemd=false/a command=/opt/scp-workers/runtime-start' /etc/wsl.conf"
        if ($LASTEXITCODE -ne 0) { throw 'SCP-Worker volatile runtime configuration failed' }
    }
    wsl --terminate $name
    if ($LASTEXITCODE -ne 0) { throw "Dedicated $name termination failed" }
    wsl -d $name -u scp --cd / --exec sh -c 'test -z "$WSL_INTEROP" && ! mount | grep -q " type 9p .*path=[A-Za-z]:" && test ! -d /mnt/c/Windows'
    if ($LASTEXITCODE -ne 0) { throw "$name automount/interop isolation verification failed" }
    if ($GoArchive) {
        $installGo = @'
import pathlib, subprocess, sys
with pathlib.Path(sys.argv[1]).open('rb') as archive:
    subprocess.run(['wsl.exe', '-d', sys.argv[2], '-u', 'root', '--cd', '/', '--exec', 'sh', '-c', 'test ! -e /usr/local/go && tar -xz -C /usr/local && ln -s /usr/local/go/bin/go /usr/local/bin/go && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt'], stdin=archive, check=True, timeout=120)
'@
        python -c $installGo $GoArchive $name
        if ($LASTEXITCODE -ne 0) { throw "$name toolchain installation failed; existing installations are never overwritten." }
    }
    wsl -d $name -u scp --cd / --exec sh -c 'command -v go >/dev/null && go version && command -v git >/dev/null && command -v python3 >/dev/null'
    if ($LASTEXITCODE -ne 0) { throw "$name requires preinstalled Go 1.26+, Git and Python. Provision the verified toolchain before starting Attempts/CI." }
    Write-Output "Dedicated $name WSL2 isolation and toolchain verified."
}
