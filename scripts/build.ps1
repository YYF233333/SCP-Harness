$ErrorActionPreference = 'Stop'
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    go build -trimpath -buildvcs=true -o scp.exe ./cmd/scp
    if ($LASTEXITCODE -ne 0) { throw 'scp.exe build failed' }
    Get-FileHash -Algorithm SHA256 -LiteralPath '.\scp.exe'
} finally { Pop-Location }
