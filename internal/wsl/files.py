"""Core-owned bounded transfer helper; the worker cannot configure these bounds."""
import json, os, pwd, stat, sys, tarfile

op, root = sys.argv[1:3]
if root != '/scp/attempt':
    raise SystemExit(42)

def fail(message):
    print(message, file=sys.stderr)
    raise SystemExit(42)

def scan(base, limits, exclude_git=False):
    count = total = 0
    stack = [(base, '')]
    while stack:
        directory, prefix = stack.pop()
        with os.scandir(directory) as entries:
            for entry in entries:
                name = prefix + entry.name
                if exclude_git and name == '.git' and entry.is_dir(follow_symlinks=False):
                    continue
                info = entry.stat(follow_symlinks=False)
                count += 1
                if count > limits['workspace_max_files'] or len(name.encode()) > limits['path_max_bytes']:
                    fail('workspace count/path bound')
                if not stat.S_ISREG(info.st_mode) and not stat.S_ISDIR(info.st_mode):
                    fail('unsupported special file: ' + name)
                if stat.S_ISREG(info.st_mode):
                    if info.st_nlink != 1:
                        fail('unsupported hard link: ' + name)
                    total += info.st_size
                    if info.st_size > limits['single_file_max_bytes'] or total > limits['workspace_max_bytes']:
                        fail('workspace byte bound')
                yield entry.path, name, info
                if stat.S_ISDIR(info.st_mode):
                    stack.append((entry.path, name + '/'))

def discard(base, limits):
    # Bounded, incremental deletion only within Core's exact transient root.
    # Never follows symlinks; on an overflow the next invocation can finish
    # the remainder without an unbounded traversal.
    count = total = 0
    stack = [(base, False)]
    while stack:
        filename, visited = stack.pop()
        if visited:
            os.rmdir(filename)
            continue
        if not os.path.lexists(filename):
            continue
        info = os.lstat(filename)
        count += 1
        total += info.st_size if stat.S_ISREG(info.st_mode) else 0
        if count > limits['workspace_max_files'] or total > limits['workspace_max_bytes'] or len(os.path.relpath(filename, base).encode()) > limits['path_max_bytes']:
            fail('transient cleanup bound exceeded')
        if stat.S_ISDIR(info.st_mode):
            stack.append((filename, True))
            with os.scandir(filename) as entries:
                for entry in entries:
                    if len(stack) + count > limits['workspace_max_files']:
                        fail('transient cleanup inventory bound exceeded')
                    stack.append((entry.path, False))
        else:
            os.unlink(filename)

if op == 'prepare':
    limits = json.loads(sys.argv[3])
    # A new inode tree each time; only this dedicated transient directory is removed.
    if os.path.lexists(root):
        if os.path.islink(root):
            fail('unexpected root symlink')
        discard(root, limits)
    os.mkdir(root, 0o755)
    with tarfile.open(fileobj=sys.stdin.buffer, mode='r|') as archive:
        for member in archive:
            if not member.isfile() and not member.isdir():
                fail('unsupported incoming archive member')
            archive.extract(member, root, filter='data')
    uid = pwd.getpwnam('scp').pw_uid
    os.chown(root + '/result.json', uid, uid)
    os.chmod(root + '/result.json', 0o600)
    os.chmod(root + '/input.json', 0o444)
elif op == 'restore':
    with tarfile.open(fileobj=sys.stdin.buffer, mode='r|') as archive:
        for member in archive:
            if not member.isfile() and not member.isdir():
                fail('unsupported snapshot member')
            archive.extract(member, root + '/workspace', filter='data')
            for channel in ('artifact.content', 'repository.snapshot'):
                filename = root + '/context/' + channel + '/' + member.name
                if os.path.exists(filename):
                    os.chmod(filename, member.mode & 0o777)
elif op == 'permissions':
    limits, mode = json.loads(sys.argv[3]), sys.argv[4]
    uid = pwd.getpwnam('scp').pw_uid
    for area in ('context', 'workspace'):
        base = root + '/' + area
        if not os.path.isdir(base):
            continue
        writable = area == 'workspace' and mode == 'writable'
        for filename, name, info in scan(base, limits):
            os.chown(filename, uid if writable else 0, uid if writable else 0)
            os.chmod(filename, (0o755 if stat.S_ISDIR(info.st_mode) else 0o644 | (info.st_mode & 0o111)) if writable else (0o555 if stat.S_ISDIR(info.st_mode) else 0o444 | (info.st_mode & 0o111)))
        os.chown(base, uid if writable else 0, uid if writable else 0)
        os.chmod(base, 0o755 if writable else 0o555)
elif op == 'capture':
    limits = json.loads(sys.argv[3])
    try:
        with open(root + '/input.json', 'rb') as f:
            owner = json.load(f).get('attempt_id')
    except (OSError, ValueError):
        fail('no capture-ready workspace')
    if owner != sys.argv[4]:
        fail('workspace belongs to a different Attempt')
    base = root + '/workspace'
    if not os.path.isdir(base) or os.path.islink(base):
        fail('workspace missing or invalid')
    # Validate the entire bounded inventory before publishing any tar bytes.
    files = list(scan(base, limits, True))
    with tarfile.open(fileobj=sys.stdout.buffer, mode='w|', format=tarfile.PAX_FORMAT) as archive:
        for filename, name, info in files:
            archive.add(filename, arcname=name, recursive=False)
elif op == 'result':
    cap = int(sys.argv[3])
    filename = root + '/result.json'
    try:
        info = os.lstat(filename)
    except FileNotFoundError:
        raise SystemExit(0)
    if not stat.S_ISREG(info.st_mode) or info.st_size > cap:
        fail('result exceeds bound or is not regular')
    with open(filename, 'rb') as f:
        sys.stdout.buffer.write(f.read(cap + 1))
elif op == 'exists':
    print('yes' if os.path.isdir(root + '/workspace') else 'no')
elif op == 'discard':
    discard(root, json.loads(sys.argv[3]))
else:
    fail('unknown helper operation')
