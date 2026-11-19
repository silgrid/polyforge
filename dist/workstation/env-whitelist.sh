# dist/workstation/env-whitelist.sh — the workstation container's
# environment boundary (polyforge#161, 84-image), as a function the
# entrypoint sources and the test suite executes (the real filter, never
# prose). AC3: the container's environment is compose's explicit list
# plus the PI_* / PI_WEB_* families — PI_TEST_* never crosses — and this
# whitelist is the second guard: everything the caller did not name
# goes. It also fixes the image's own two values: BUILDAH_ISOLATION=chroot
# (buildah's build-network setup opens a non-dumpable runtime init's
# netns, which needs CAP_SYS_PTRACE, and the workstation's minimal cap
# set deliberately carries none — the chroot isolation builds without
# opening any namespace; the daemon's environment allow-list carries
# the name to every step) and PORT with the Pi Web default.
workstation_env_whitelist() {
	# the names come from `env`, so a compose-added PI_* variable crosses
	# without this script knowing it; the $(…) list was computed in a
	# subshell, the unsets run in the caller's shell.
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
}
