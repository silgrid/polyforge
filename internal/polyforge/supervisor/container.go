package supervisor

// container.go (polyforge#160, Q2 C): what "under the container
// supervisor" means. The workstation container's entry point runs
// `pf supervise`, which stamps every child it starts — the daemon first
// of all — with POLYFORGE_SUPERVISOR=container:pf-supervise (the same
// marker variable the tmux loop sets to tmux:<session>). Being under
// that supervision is the marker variable AND a container marker file:
// the file alone is true inside ANY container (a verification container,
// a dev container), and the variable alone could be inherited by a host
// shell — only both together say this process was started by pf
// supervise inside a container.

// MarkerContainerPrefix is the MarkerEnv value prefix the container
// supervisor stamps its children with.
const MarkerContainerPrefix = "container:"

// MarkerContainerValue is the value pf supervise writes into the
// children's environment.
const MarkerContainerValue = MarkerContainerPrefix + "pf-supervise"

// ContainerMarkerFiles are the files whose presence says "this process
// runs inside a container" (podman's /run/.containerenv, docker's
// /.dockerenv). A package variable so tests can point the probe at
// files they own.
var ContainerMarkerFiles = []string{"/run/.containerenv", "/.dockerenv"}

// InContainer answers whether this process runs inside a container
// (podman's or docker's marker file exists).
func InContainer() bool {
	return false
}

// ContainerSupervisedEnv answers whether environ — a process's whole
// environment — carries the container supervisor's marker AND this
// process is inside a container (Q2 C: both together, never either
// alone).
func ContainerSupervisedEnv(environ []string) bool {
	return false
}
