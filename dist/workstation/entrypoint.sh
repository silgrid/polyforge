#!/bin/sh
# dist/workstation/entrypoint.sh — the workstation container's entry
# point (polyforge#161, 84-image), run by catatonit as PID 1's child.
# Everything here runs as root (the settled "first version is all
# root"). Two jobs, then `pf supervise` (#160) owns the rest:
#
# 1. The private writable cgroup namespace. Docker mounts /sys/fs/cgroup
#    read-only and its root still holds PID 1; on cgroup v2 a domain
#    controller (memory) can be enabled on a cgroup's subtree_control
#    only while NO process sits directly in it — measured: +memory
#    answers EBUSY until PID 1 and this script both moved into the
#    init/ sub-cgroup. podman (cgroup_manager=cgroupfs) then creates
#    its container groups below the root and memory.max/pids.max take
#    effect (AC2). cpu/io are best effort: a kernel without them must
#    not brick the boot, but memory and pids are the pair the
#    verification limits need, so their failure ends the run loudly.
#
# 2. The container's environment boundary (AC3): the container starts
#    with exactly what compose listed, and the boundary here is a
#    whitelist — everything the process did not name goes. What stays:
#    the fixed set (PATH, HOME, TERM), this image's one fixed value
#    (BUILDAH_ISOLATION=chroot — buildah's build-network setup opens a
#    non-dumpable runtime init's netns, which needs CAP_SYS_PTRACE, and
#    the workstation's minimal cap set deliberately has none: the
#    chroot isolation builds without opening any namespace; the daemon
#    allow-list carries the name to every step), PORT with the Pi Web
#    default, and the PI_* / PI_WEB_* families — except PI_TEST_*,
#    which never crosses the boundary. `pf supervise` hands its own
#    environment to the Pi Web child (#160's interface), so what this
#    script keeps is exactly what Pi Web sees.
set -eu

CG=/sys/fs/cgroup

# 1. cgroup: remount rw, move every boot process into init/, enable
if ! test -w "$CG/cgroup.subtree_control"; then
	mount -o remount,rw "$CG"
fi
mkdir -p "$CG/init"
echo 1 > "$CG/init/cgroup.procs"
echo $$ > "$CG/init/cgroup.procs"
echo +memory > "$CG/cgroup.subtree_control"
echo +pids > "$CG/cgroup.subtree_control"
for ctl in cpu io; do
	echo "+$ctl" > "$CG/cgroup.subtree_control" 2>/dev/null || true
done

# 2. environment: unset everything outside the whitelist (the names
# come from `env`, so a compose-added PI_* variable crosses without
# this script knowing it; unset runs in this shell, the $(…) list was
# computed in a subshell).
for name in $(env | cut -d= -f1); do
	case "$name" in
	PATH | HOME | TERM) ;;
	PI_TEST_*) unset "$name" ;;
	PI_*) ;;
	*) unset "$name" ;;
	esac
done
export BUILDAH_ISOLATION=chroot
: "${PORT:=30141}"
export PORT

exec pf supervise "$@"
