"""Synthetic integration fixture, run by the real model in its OS account."""
import errno
import json
import os
from pathlib import Path
import urllib.request

request = json.loads(Path(os.environ['SCP_INPUT']).read_text())
workspace = Path(os.environ['SCP_WORKSPACE'])
context = Path(os.environ['SCP_CONTEXT'])
mode = request['workspace']['mode']
assert os.geteuid() != 0
assert 'CONTEXT-boundary-v1' in (context / 'task.objective.json').read_text()
assert os.access(os.environ['SCP_RESULT'], os.W_OK)
denied = []
targets = [Path(os.environ['SCP_INPUT']), context / 'task.objective.json']
if mode == 'readonly':
    targets += [workspace / 'greeting.py', workspace / 'new-file', workspace / '.git']
for path in targets:
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    except OSError as error:
        assert error.errno == errno.EACCES, (path, error)
        denied.append(str(path))
    else:
        os.close(fd)
        raise AssertionError('OS allowed write: ' + str(path))
for key in ('HOME', 'CODEX_HOME', 'TMPDIR', 'XDG_CACHE_HOME', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME'):
    assert os.environ[key].startswith('/tmp/scp-codex-' + request['attempt_id'])
with urllib.request.urlopen('https://example.com', timeout=30) as response:
    assert response.status == 200
    assert len(response.read()) > 0
report = dict(mode=mode, network_http=200, context_read=True, errno='EACCES', denied=denied)
if mode == 'writable':
    (workspace / 'boundary.json').write_text(json.dumps(report))
else:
    assert not (workspace / '.git').exists()
    print('SCP_READONLY_OK ' + json.dumps(report), flush=True)
