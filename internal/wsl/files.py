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
    # Deletion is not workspace admission: neither file contents nor full paths
    # need to fit capture limits. Each incomplete batch unlinks actual entries.
    # Open relative to directory FDs so long paths do not prevent cleanup.
    budget = limits['workspace_max_files']
    if budget <= 0:
        raise ValueError('non-positive cleanup work budget')
    if not os.path.lexists(base):
        return True
    if not stat.S_ISDIR(os.lstat(base).st_mode):
        os.unlink(base)  # Also unlinks a symlink without following its target.
        return True
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    fd = os.open(base, flags)
    names = []
    removed = 0
    try:
        while removed < budget:
            with os.scandir(fd) as entries:
                entry = next(entries, None)
            if entry is None:
                if not names:
                    os.close(fd)
                    fd = None
                    os.rmdir(base)
                    return True
                parent = os.open('..', flags, dir_fd=fd)
                os.close(fd)
                fd = parent
                os.rmdir(names.pop(), dir_fd=fd)
                removed += 1
            elif entry.is_dir(follow_symlinks=False):
                child = os.open(entry.name, flags, dir_fd=fd)
                os.close(fd)
                fd = child
                names.append(entry.name)
            else:
                os.unlink(entry.name, dir_fd=fd)
                removed += 1
        return False  # budget > 0 actual deletions; caller continues boundedly.
    finally:
        if fd is not None:
            os.close(fd)

if op == 'prepare':
    # Runner must finish bounded cleanup before consuming the incoming bundle.
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
    if not discard(root, json.loads(sys.argv[3])):
        raise SystemExit(43)
else:
    fail('unknown helper operation')
