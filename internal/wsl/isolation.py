"""Admission check for the dedicated worker OS account, independent of provider."""
import os
import pwd
import stat

account = pwd.getpwnam('scp')
assert os.geteuid() == 0 and account.pw_uid != 0
assert os.getgrouplist('scp', account.pw_gid) == [account.pw_gid], 'unexpected supplementary authority'
os.setgroups([account.pw_gid])
os.setegid(account.pw_gid)

def accessible(path, mode):
    # Enumerate as Core so searchable-but-unlistable directories cannot hide
    # writable children. Evaluate access using the actual worker OS credentials.
    os.seteuid(account.pw_uid)
    try:
        return os.access(path, mode, effective_ids=True)
    finally:
        os.seteuid(0)
mounts = {}
with open('/proc/mounts') as source:
    for line in source:
        device, path, kind, options, *_ = line.split()
        mounts[path] = (kind, options.split(','))
for path in ('/home/scp', '/tmp', '/var/tmp', '/run', '/dev/shm', '/run/shm'):
    assert mounts.get(path, ('',))[0] == 'tmpfs', 'nonvolatile runtime: ' + path
assert not accessible('/mnt', os.X_OK), 'shared WSL mounts exposed'
assert not os.environ.get('WSL_INTEROP'), 'interop environment enabled'
assert not accessible('/run/WSL', os.X_OK), 'interop sockets exposed'

# Inspect effective access, including group/world permissions, rather than just
# ownership. Inaccessible root-owned archives need not be modified or deleted.
# /scp/attempt is the one persistent output tree managed by Core's bounded cleanup.
excluded = {'/proc', '/sys', '/dev', '/run', '/mnt', '/home/scp', '/tmp', '/var/tmp', '/scp/attempt'}
# Readonly mounts cannot hold mutable state even when an inode is scp-owned.
# In particular, do not traverse WSL's large readonly Windows driver share.
excluded.update(path for path, (_, options) in mounts.items() if 'ro' in options)
for directory, children, files in os.walk('/', followlinks=False):
    children[:] = [name for name in children if directory.rstrip('/') + '/' + name not in excluded]
    for path in [directory] + [os.path.join(directory, name) for name in children + files]:
        if path in excluded:
            continue
        info = os.lstat(path)
        if info.st_uid == account.pw_uid:
            raise RuntimeError('persistent worker-owned path: ' + path)
        if not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)):
            continue
        # An owner can chmod a currently unwritable inode back to writable.
        if accessible(path, os.W_OK):
            raise RuntimeError('persistent worker-writable path: ' + path)
    children[:] = [name for name in children if accessible(os.path.join(directory, name), os.X_OK)]
