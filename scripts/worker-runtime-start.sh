#!/bin/sh
set -eu
# Dedicated SCP-Worker only. These mounts die with wsl --terminate; no
# provider sandbox participates in the authority or lifetime boundary.
mkdir -p /home/scp /mnt /var/crash /run/WSL
chown root:root /home/scp /mnt /var/crash /run/WSL
chmod 700 /home/scp /mnt /var/crash /run/WSL
for directory in /tmp /var/tmp /home/scp; do
    if ! mountpoint -q "$directory"; then
        mount -t tmpfs -o mode=1777,nosuid,nodev tmpfs "$directory"
    fi
    test "$(findmnt -n -o FSTYPE --target "$directory")" = tmpfs
done
chown scp:scp /home/scp
chmod 700 /home/scp
touch /run/scp-worker-ready
