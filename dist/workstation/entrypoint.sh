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
# 2. The container's environment boundary (AC3): the whitelist lives in
#    workstation-env-whitelist.sh — sourced so the test suite executes
#    the real filter, not this file's prose. It keeps the fixed set
#    (PATH, HOME, TERM), the image's one fixed value
#    (BUILDAH_ISOLATION=chroot), PORT with the Pi Web default, and the
#    PI_* / PI_WEB_* families — PI_TEST_* never crosses. `pf supervise`
#    hands its own environment to the Pi Web child (#160's interface),
#    so what this script keeps is exactly what Pi Web sees.
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

# 2. environment: the boundary whitelist, sourced and executed
. /usr/local/bin/workstation-env-whitelist.sh
workstation_env_whitelist

# 3. first boot (polyforge#176, 84-components-C): the one-time
#    initialization — the seed install into the components volume, the
#    AIHub identity (the daily user, its key, the local project),
#    cli.toml's default Cell, the harness references and the phase
#    record — before supervise starts the server, so the server's own
#    fresh-install bootstrap never fires. Idempotent: a completed
#    first boot skips itself, and an interrupted one resumes (the
#    record says which phase stopped). A refusal here ends the
#    container: starting supervise on an uninitialized or
#    inconsistent machine is never better.
pf first-boot

exec pf supervise "$@"
