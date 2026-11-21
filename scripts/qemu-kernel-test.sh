#!/bin/bash
# Run the tool package's memory-guard and cgroup tests inside a real Linux kernel
# under qemu (no KVM needed; software emulation is enough). Containers share the
# host kernel, so this is the way to check older or newer kernels.
#
#   scripts/qemu-kernel-test.sh <path-to-bzImage/vmlinuz> [go test -run regexp]
#
# Needs: go, qemu-system-x86_64, busybox (static), cpio, gzip. The kernel must have
# cgroup v2 and a serial console built in (distro kernels such as Ubuntu 20.04's
# 5.4 and Rocky 9's 5.14 do). Used to verify: 5.4 (Spawn falls back, sampler guards),
# 5.14.0-741.el9 (kernel tier works, memory.peak present).
set -euo pipefail
kernel=${1:?usage: $0 <kernel image> [test regexp]}
run=${2:-Cgroup|SpawnFallsBack|RunBashSyncIsGuarded|MemGuardKillsRealProcess}
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
CGO_ENABLED=0 go test -c -o "$work/tool" "$root/tool"
mkdir -p "$work/r"/{bin,proc,sys,dev,tmp,t}
cp "$(command -v busybox)" "$work/r/bin/"
for c in sh sleep cat echo mount mkdir setsid ls uname grep kill poweroff pgrep ps; do ln -s busybox "$work/r/bin/$c"; done
printf '#!/bin/busybox sh\nexec /bin/busybox sh "$@"\n' > "$work/r/bin/bash"; chmod +x "$work/r/bin/bash"
cp "$work/tool" "$work/r/t/tool"
cat > "$work/r/init" <<EOS
#!/bin/busybox sh
export PATH=/bin
mount -t proc none /proc; mount -t sysfs none /sys; mount -t devtmpfs none /dev 2>/dev/null
mount -t cgroup2 none /sys/fs/cgroup
echo "KERNEL \$(uname -r)"
cd /t && ./tool -test.count=1 -test.run '$run' -test.v 2>&1 | grep -E '^(---|ok|PASS|FAIL|panic)|SKIP|_test.go'
echo VMDONE
poweroff -f
EOS
chmod +x "$work/r/init"
(cd "$work/r" && find . | cpio -o -H newc --quiet | gzip > "$work/initrd.gz")
timeout 600 qemu-system-x86_64 -m 2048 -smp 2 -nographic -no-reboot -kernel "$kernel" \
  -initrd "$work/initrd.gz" -append "console=ttyS0 panic=-1 rdinit=/init" 2>&1 |
  grep -aE '^(KERNEL|---|ok|PASS|FAIL|panic|VMDONE)|SKIP|_test.go'
