param([ValidateSet('SCP-Worker', 'SCP-Test')][string[]]$Distro = @('SCP-Worker', 'SCP-Test'))
$ErrorActionPreference = 'Stop'
$env:WSL_UTF8 = '1'
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
    Write-Output "Dedicated $name WSL2 isolation verified."
}
