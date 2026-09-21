param()
$ErrorActionPreference = 'Stop'
$projectPath = Split-Path -Parent $PSScriptRoot
if (Get-Process -Name scp -ErrorAction SilentlyContinue) { throw 'Stop the normal SCP scheduler before provisioning its test environment.' }
$seedModules = @'
import json, os, pathlib, subprocess, sys, tarfile
root = pathlib.Path(sys.argv[1]).resolve()
offline = dict(os.environ, GOPROXY='off')
print('Verifying the existing Windows module cache...', flush=True)
subprocess.run(['go', 'mod', 'verify'], cwd=root, env=offline, check=True, timeout=120)
manifest = json.loads(subprocess.check_output(['go', 'mod', 'edit', '-json'], cwd=root, env=offline, text=True))
cache = pathlib.Path(subprocess.check_output(['go', 'env', 'GOMODCACHE'], cwd=root, text=True).strip()) / 'cache' / 'download'
bundle = root / '.local' / 'bootstrap' / 'go-modules.tar'
bundle.parent.mkdir(parents=True, exist_ok=True)
with tarfile.open(bundle, 'w') as archive:
    for module in manifest['Require']:
        escaped = ''.join('!' + c.lower() if 'A' <= c <= 'Z' else c for c in module['Path'])
        prefix = cache / escaped / '@v' / module['Version']
        for suffix in ('.mod', '.zip', '.info'):
            source = pathlib.Path(str(prefix) + suffix)
            if not source.is_file():
                if suffix == '.info':
                    continue
                raise RuntimeError('Missing cached dependency: ' + str(source) + '. Run go mod download in the Windows project first.')
            archive.add(source, arcname=source.relative_to(cache).as_posix(), recursive=False)
print('Transferring ' + str(len(manifest['Require'])) + ' pinned modules to SCP-Test...', flush=True)
wsl = ['wsl.exe', '-d', 'SCP-Test', '-u', 'scp', '--cd', '/', '--exec']
target = subprocess.check_output(wsl + ['go', 'env', 'GOMODCACHE'], text=True, timeout=30).strip() + '/cache/download'
subprocess.run(wsl + ['mkdir', '-p', target], check=True, timeout=30)
with bundle.open('rb') as archive:
    subprocess.run(wsl + ['tar', '-xf', '-', '-C', target], stdin=archive, check=True, timeout=120)
print('Module archives installed. Linux Go will check them against go.sum on build; TLS and checksum settings are unchanged.', flush=True)
'@
python -c $seedModules $projectPath
if ($LASTEXITCODE -ne 0) { throw 'SCP-Test module cache provisioning failed' }
