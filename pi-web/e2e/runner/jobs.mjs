// Browser-pool width for e2e/all.mjs. Lives under e2e/runner/,
// outside the e2e/*.mjs suite glob, so neither the runner nor the release
// verify-scope engine counts it as a suite.
//
// Default: min(6, max(2, floor(cpus / 2))) — an 8-cpu verification container
// runs 4 browser suites at a time, 12 cores and up run 6, 4 cores and fewer keep 2.
// The former floor(cpus / 4) (2 on 8 cpus) dates from each suite compiling its
// own `next dev` (E2E_JOBS=4 on a 12-core box then pushed suites over their
// page-load timeouts); the bar now serves a build made once
// (`next start`), and at pool 2 its suites used about 1.4 of 8 cores
// (owner decision). cpuCount is the CPUs the
// runner may use (os.availableParallelism(), which follows a container's CPU limit;
// os.cpus() reports the host's cores). An explicit E2E_JOBS wins; blank, 0 or
// non-numeric falls through to the default.

export function defaultJobs(cpuCount) {
  return Math.min(6, Math.max(2, Math.floor(cpuCount / 2)));
}

export function resolveJobs(envValue, cpuCount) {
  return Math.max(1, Number(envValue) || defaultJobs(cpuCount));
}
