$ErrorActionPreference = 'Stop'
$project = Split-Path -Parent $PSScriptRoot
$output = Join-Path $project '.local\fake-worker'
New-Item -ItemType Directory -Path (Split-Path -Parent $output) -Force | Out-Null
$previousOS = $env:GOOS
$previousArch = $env:GOARCH
try {
    $env:GOOS='linux'
    $env:GOARCH='amd64'
    go build -trimpath -o $output (Join-Path $project 'testdata\workers\main.go')
    if ($LASTEXITCODE -ne 0) { throw 'Fixture worker build failed' }
} finally { $env:GOOS=$previousOS; $env:GOARCH=$previousArch }
# Use a byte-preserving pipe; PowerShell's object pipeline is not binary-safe.
$python = @'
import pathlib, subprocess, sys
p = pathlib.Path(sys.argv[1])
with p.open('rb') as f:
    subprocess.run(['wsl.exe', '-d', 'SCP-Worker', '-u', 'root', '--cd', '/', '--exec', 'sh', '-c', 'cat > /opt/scp-workers/fake-worker && chmod 755 /opt/scp-workers/fake-worker'], stdin=f, check=True, timeout=60)
'@
python -c $python $output
if ($LASTEXITCODE -ne 0) { throw 'Fixture worker installation failed' }
