param()
$ErrorActionPreference = 'Stop'
$env:WSL_UTF8 = '1'
$listing = (wsl --list --verbose | Out-String) -replace "`0", ''
if ($LASTEXITCODE -ne 0 -or $listing -notmatch 'SCP-Worker\s+\S+\s+2') { throw 'Dedicated WSL2 SCP-Worker must already exist. See docs/operations.md for explicit import steps.' }
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
$script -replace "`r", '' | wsl -d SCP-Worker -u root --cd / --exec sh -c "sed 's/\r$//' | sh"
if ($LASTEXITCODE -ne 0) { throw 'Dedicated distro configuration failed' }
wsl --terminate SCP-Worker
if ($LASTEXITCODE -ne 0) { throw 'Dedicated distro termination failed' }
wsl -d SCP-Worker -u scp --cd / --exec sh -c 'test -z "$WSL_INTEROP" && ! mount | grep -q " type 9p .*path=[A-Za-z]:" && test ! -d /mnt/c/Windows'
if ($LASTEXITCODE -ne 0) { throw 'Automount/interop isolation verification failed' }
Write-Output 'Dedicated SCP-Worker WSL2 isolation verified.'
