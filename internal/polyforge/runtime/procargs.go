package runtime

// procargs.go: the kern.procargs2 parser (polyforge#218). Only
// procinfo_darwin.go feeds it macos blobs; this file carries no build
// tag so its unit tests run on Linux with synthetic blobs.
//
// kern.procargs2 layout (XNU, kern_proc.c): the first four bytes are
// the argument count (little-endian; darwin is little-endian), then
// one string per buffer region — the exec path first, then the argv
// strings (pad NULs may follow the exec path), then the environment
// strings NUL-terminated up to the buffer's end (trailing pad NULs).
// That layout is the EXEC-TIME snapshot the kernel reads back from
// live memory: a process that rewrites its argument block afterwards
// (node moves argv+env and zeroes the original) leaves the strings at
// an arbitrary offset, so the parser skips pad NULs throughout
// instead of assuming contiguous regions (see procargsEnviron).

import (
	"encoding/binary"
	"strings"
)

// procargsEnviron parses one kern.procargs2 blob into its environment
// strings (KEY=VALUE form, never empty). Unparsable blobs answer what
// they parse up to that point; the exact-token marker matching in the
// callers then simply finds nothing (fail-closed on the guard's side).
func procargsEnviron(raw []byte) []string {
	if len(raw) < 4 {
		return nil
	}
	argc := binary.LittleEndian.Uint32(raw)
	lines := strings.Split(string(raw[4:]), "\x00")
	// lines[0] is the exec path; skip the pad NULs that may follow it
	lines = lines[1:]
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	// the next argc strings are argv
	if c := min(int(argc), len(lines)); c > 0 {
		lines = lines[c:]
	}
	// the environment: every remaining non-empty string, with pad NULs
	// SKIPPED, never terminated on. Two real-blob realities force this:
	// the exec-time argv+env block is LIVE memory the process may
	// rewrite after exec (node moves argv+env to its own storage and
	// zeroes the original, leaving the environment strings at an
	// arbitrary offset behind a stretch of NULs), and the kernel appends
	// its platform strings (ptr_munge=, main_stack=, executable_file=,
	// …) after the environment. Every marker consumer therefore
	// matches exact KEY=VALUE tokens (envHasMarkers/stepMarkers), so the
	// extra argv/platform strings a rewritten blob mixes in are inert;
	// an environment that is truly gone yields no markers, which the
	// callers fail closed on.
	env := make([]string, 0, len(lines))
	for _, l := range lines {
		if l == "" {
			continue
		}
		env = append(env, l)
	}
	return env
}
