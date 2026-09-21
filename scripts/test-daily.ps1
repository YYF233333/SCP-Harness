param()
$ErrorActionPreference = 'Stop'
$projectPath = Split-Path -Parent $PSScriptRoot
if (Get-Process -Name scp -ErrorAction SilentlyContinue) { throw 'Stop the normal SCP scheduler before using its dedicated SCP-Test environment.' }
$evidencePath = Join-Path $projectPath ('.local\daily\' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
New-Item -ItemType Directory -Path $evidencePath -Force | Out-Null
$runDaily = @'
import hashlib, json, pathlib, subprocess, sys, tarfile
root, evidence = (pathlib.Path(p).resolve() for p in sys.argv[1:3])
print('[1/4] Packing source files. Logs: ' + str(evidence), flush=True)
names = subprocess.check_output(['git', 'ls-files', '-cz', '--others', '--exclude-standard'], cwd=root).split(b'\0')
snapshot = evidence / 'source.tar'
with tarfile.open(snapshot, 'w') as archive:
    for raw in sorted(set(names)):
        if not raw:
            continue
        name = raw.decode('utf-8')
        path = root / name
        if path.is_file():
            archive.add(path, arcname=name, recursive=False)
wsl = ['wsl.exe', '-d', 'SCP-Test', '-u', 'scp', '--cd', '/', '--exec']
print('[2/4] Transferring the execution snapshot to SCP-Test...', flush=True)
workspace = subprocess.check_output(wsl + ['mktemp', '-d', '/tmp/scp-daily.XXXXXXXX'], text=True).strip()
with snapshot.open('rb') as archive:
    subprocess.run(wsl + ['tar', '-xf', '-', '-C', workspace], stdin=archive, check=True)
wsl = ['wsl.exe', '-d', 'SCP-Test', '-u', 'scp', '--cd', workspace, '--exec']
print('[3/4] Running go test. Progress: go-test.jsonl; dependency/build diagnostics: go-test.stderr.log', flush=True)
with (evidence / 'go-test.jsonl').open('wb') as out, (evidence / 'go-test.stderr.log').open('wb') as err:
    tested = subprocess.run(wsl + ['go', 'test', './...', '-count=1', '-json', '-timeout=30m'], stdout=out, stderr=err)
print('go test exit code: ' + str(tested.returncode), flush=True)
if tested.returncode:
    print((evidence / 'go-test.stderr.log').read_text(errors='replace')[-4000:], flush=True)
    for line in (evidence / 'go-test.jsonl').read_text(errors='replace').splitlines():
        event = json.loads(line)
        if event.get('Action') == 'build-output':
            print(event.get('Output', '').rstrip(), flush=True)
        elif event.get('Action') == 'fail':
            print('FAIL: ' + event.get('Package', '') + ' ' + event.get('Test', ''), flush=True)
print('[4/4] Running go vet. Log: go-vet.log', flush=True)
with (evidence / 'go-vet.log').open('wb') as out:
    vetted = subprocess.run(wsl + ['go', 'vet', './...'], stdout=out, stderr=subprocess.STDOUT)
if vetted.returncode:
    print((evidence / 'go-vet.log').read_text(errors='replace')[-4000:], flush=True)
report = dict(suite='daily-linux', source_sha256=hashlib.sha256(snapshot.read_bytes()).hexdigest(), test_exit=tested.returncode, vet_exit=vetted.returncode, disposable_workspace=workspace)
(evidence / 'result.json').write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
print(json.dumps(report, indent=2))
print('Execution snapshot retained for manual cleanup; development remains in the original Windows repository.')
sys.exit(tested.returncode or vetted.returncode)
'@
python -c $runDaily $projectPath $evidencePath
if ($LASTEXITCODE -ne 0) { throw "Daily Linux verification failed. Evidence: $evidencePath" }
Write-Output "Daily Linux test and vet passed. Evidence: $evidencePath"
