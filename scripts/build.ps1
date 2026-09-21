$ErrorActionPreference = 'Stop'
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $output = Join-Path (Get-Location) '.local\build\scp.exe'
    New-Item -ItemType Directory -Path (Split-Path -Parent $output) -Force | Out-Null
    go build -trimpath -buildvcs=true -o $output ./cmd/scp
    if ($LASTEXITCODE -ne 0) { throw 'scp.exe build failed' }
    Get-FileHash -Algorithm SHA256 -LiteralPath $output
} finally { Pop-Location }
