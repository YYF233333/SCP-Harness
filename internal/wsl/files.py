"""Core-owned bounded transfer helper; the worker cannot configure these bounds."""
import json, os, pwd, stat, sys, tarfile

op, root = sys.argv[1:3]
if root != '/scp/attempt' and not (os.path.isabs(root) and os.path.basename(root).endswith(('.local-worker', '.local-test'))):
    raise SystemExit(42)

local = os.geteuid() != 0
uid = os.getuid() if local else pwd.getpwnam('scp').pw_uid

def own(filename, writable):
    if not local:
        os.chown(filename, uid if writable else 0, uid if writable else 0)

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

def identity(info):
    return (info.st_dev, info.st_ino, info.st_mode, info.st_nlink,
            info.st_size, info.st_mtime_ns, info.st_ctime_ns)

def scan_fd(root_fd, limits):
    # Names are archive labels only. Every lookup is relative to a pinned
    # directory, including both inventories used by live observation.
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    stack = []
    count = total = 0

    def enter(fd, prefix, expected, entry_name):
        try:
            if identity(os.fstat(fd)) != expected:
                fail('live workspace changed during observation; retry')
            entries = os.scandir(fd)
        except BaseException:
            os.close(fd)
            raise
        stack.append((fd, prefix, expected, entries, entry_name))

    try:
        # A fresh directory description avoids sharing enumeration offsets with
        # root_fd or the other inventory; '.' is resolved only through root_fd.
        enter(os.open('.', flags, dir_fd=root_fd), '', identity(os.fstat(root_fd)), None)
        while stack:
            fd, prefix, expected, entries, entry_name = stack[-1]
            entry = next(entries, None)
            if entry is None:
                if identity(os.fstat(fd)) != expected:
                    fail('live workspace changed during observation; retry')
                if len(stack) > 1:
                    if identity(os.stat(entry_name, dir_fd=stack[-2][0], follow_symlinks=False)) != expected:
                        fail('live workspace changed during observation; retry')
                stack.pop()
                entries.close()
                os.close(fd)
                continue
            name = prefix + entry.name
            info = os.stat(entry.name, dir_fd=fd, follow_symlinks=False)
            if name == '.git' and stat.S_ISDIR(info.st_mode):
                continue
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
            yield name, info
            if stat.S_ISDIR(info.st_mode):
                enter(os.open(entry.name, flags, dir_fd=fd), name + '/', identity(info), entry.name)
    finally:
        for fd, _, _, entries, _ in reversed(stack):
            entries.close()
            os.close(fd)

def capture(base, limits, archive):
    # Both terminal capture and live observation use the same admission bounds.
    # Pin every path component without following links; concurrent replacement
    # must never let the root helper read outside the worker workspace.
    flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK
    root_fd = os.open(base, flags | os.O_DIRECTORY)
    try:
        initial_root = identity(os.fstat(root_fd))
        files = list(scan_fd(root_fd, limits))
        inventory = {name: identity(info) for name, info in files}
        for name, info in files:
            fd = os.dup(root_fd)
            try:
                parts = name.split('/')
                for i, part in enumerate(parts):
                    child = os.open(part, flags | (os.O_DIRECTORY if i < len(parts)-1 else 0), dir_fd=fd)
                    os.close(fd)
                    fd = child
                if identity(os.fstat(fd)) != identity(info):
                    fail('live workspace changed during observation; retry')
                header = tarfile.TarInfo(name)
                header.mode = stat.S_IMODE(info.st_mode)
                header.type = tarfile.DIRTYPE if stat.S_ISDIR(info.st_mode) else tarfile.REGTYPE
                header.size = 0 if stat.S_ISDIR(info.st_mode) else info.st_size
                header.mtime = info.st_mtime
                if stat.S_ISDIR(info.st_mode):
                    archive.addfile(header)
                else:
                    with os.fdopen(os.dup(fd), 'rb') as source:
                        archive.addfile(header, source)
                if identity(os.fstat(fd)) != identity(info):
                    fail('live workspace changed during observation; retry')
            finally:
                os.close(fd)
        final = {name: identity(info) for name, info in scan_fd(root_fd, limits)}
        if inventory != final or identity(os.stat(base, follow_symlinks=False)) != initial_root:
            fail('live workspace changed during observation; retry')
    finally:
        os.close(root_fd)

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
            if local:
                os.fchmod(fd, 0o700)
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
    own(root + '/result.json', True)
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
    for area in ('context', 'workspace'):
        base = root + '/' + area
        if not os.path.isdir(base):
            continue
        writable = area == 'workspace' and mode == 'writable'
        for filename, name, info in scan(base, limits):
            own(filename, writable)
            os.chmod(filename, (0o755 if stat.S_ISDIR(info.st_mode) else 0o644 | (info.st_mode & 0o111)) if writable else (0o555 if stat.S_ISDIR(info.st_mode) else 0o444 | (info.st_mode & 0o111)))
        own(base, writable)
        os.chmod(base, 0o755 if writable else 0o555)
elif op in ('capture', 'observe'):
    limits = json.loads(sys.argv[3])
    try:
        with open(root + '/input.json', 'rb') as f:
            owner = json.load(f).get('attempt_id')
    except (OSError, ValueError):
        fail('no capture-ready workspace')
    if owner != sys.argv[4]:
        fail('workspace belongs to a different Attempt')
    base = root + '/workspace'
    try:
        with tarfile.open(fileobj=sys.stdout.buffer, mode='w|', format=tarfile.PAX_FORMAT) as archive:
            capture(base, limits, archive)
        with open(root + '/input.json', 'rb') as f:
            if json.load(f).get('attempt_id') != owner:
                fail('live workspace changed during observation; retry')
    except (OSError, ValueError, tarfile.TarError):
        fail('live workspace changed during observation; retry')
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
