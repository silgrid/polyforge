package verifyrun

// rerun.go: deriving a failing unit's targeted re-run from the planned
// command that ran it. A re-run is only evidence when it runs the unit
// the way the plan did, so it keeps the command's environment
// assignments and every flag it carried (a `-race`, `-tags`, `-W error`
// dropped would turn a real failure into a "flaky" pass); a flag whose
// arity cannot be read makes the unit not re-runnable instead (fail
// closed: it stays a failure).

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// goValueFlags are the go test / go build flags that take a separate
// value word (`-tags x`); every other flag is read as boolean.
var goValueFlags = map[string]bool{
	"run": true, "skip": true, "count": true, "timeout": true, "tags": true, "p": true,
	"parallel": true, "cpu": true, "bench": true, "benchtime": true, "shuffle": true,
	"coverprofile": true, "covermode": true, "coverpkg": true, "o": true, "exec": true,
	"ldflags": true, "gcflags": true, "asmflags": true, "mod": true, "modfile": true,
	"C": true, "overlay": true, "pgo": true, "vet": true, "blockprofile": true,
	"cpuprofile": true, "memprofile": true, "mutexprofile": true, "trace": true,
	"outputdir": true, "fuzz": true, "fuzztime": true, "fuzzminimizetime": true,
	"list": true, "toolexec": true, "compiler": true, "installsuffix": true,
	"pkgdir": true, "buildmode": true, "blockprofilerate": true, "memprofilerate": true,
	"mutexprofilefraction": true,
}

// goDropFlags are the flags the targeted re-run sets itself (a planned
// -timeout is kept: a re-run without it would pass a hang).
var goDropFlags = map[string]bool{"run": true, "skip": true, "count": true, "v": true, "json": true}

// goTestFlags answers the flags of a simple `go test` command the re-run
// must carry (package patterns and the re-run's own flags dropped).
// isGoTest is false for any other command (a Makefile target, a harness);
// ok is false when the command cannot be carried (`-args`).
func goTestFlags(cmd string) (flags []string, isGoTest, ok bool) {
	words, raws, simple := shellWordsRaw(cmd)
	if !simple || !hasGoTest(words) {
		return nil, false, true
	}
	_, i := commandWord(words)
	for j := i + 2; j < len(words); j++ {
		w := words[j]
		if w == "-args" || w == "--args" {
			return nil, true, false
		}
		if !strings.HasPrefix(w, "-") {
			continue // a package pattern
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(w, "-"), "=")
		separate := !hasValue && goValueFlags[name] && j+1 < len(words)
		if goDropFlags[name] {
			if separate {
				j++
			}
			continue
		}
		flags = append(flags, raws[j])
		if separate {
			j++
			flags = append(flags, raws[j])
		}
	}
	return flags, true, true
}

// goRerun is the targeted re-run of a Go test (run = "^Name$") or a Go
// package (run = "") at target, carrying the planned command's
// assignments and flags.
func goRerun(cmd, run, target string, timeout time.Duration) (string, string) {
	flags, _, ok := goTestFlags(cmd)
	if !ok {
		return "", "not re-runnable: the planned `go test` passes arguments through -args"
	}
	parts := leadingAssignments(cmd)
	parts = append(parts, "go", "test")
	parts = append(parts, flags...)
	parts = append(parts, "-count=1", "-v")
	if !hasFlag(flagNames(flags), "-timeout") {
		parts = append(parts, "-timeout", timeout.String())
	}
	if run != "" {
		parts = append(parts, "-run", "'"+run+"'")
	}
	parts = append(parts, target)
	return strings.Join(parts, " "), ""
}

// The pytest / unittest options a re-run can carry: valueless switches
// and `--name=value` forms. Any other option may take a separate value
// whose word cannot be told from a path, so its command is not re-run.
var (
	pytestSwitches = map[string]bool{"-q": true, "-qq": true, "-qqq": true, "-v": true, "-vv": true, "-vvv": true,
		"-x": true, "-s": true, "-l": true, "--strict-markers": true, "--strict-config": true, "--no-header": true,
		"--disable-warnings": true, "--showlocals": true, "--exitfirst": true, "--quiet": true, "--verbose": true}
	unittestSwitches = map[string]bool{"-v": true, "-q": true, "-f": true, "-c": true, "-b": true, "--locals": true,
		"--verbose": true, "--quiet": true, "--failfast": true, "--catch": true, "--buffer": true}
	reAttachedOpt = regexp.MustCompile(`^(--[A-Za-z][\w-]*=.*|-r\w+|-W\S+|-p\S+)$`)
)

// pyRerun is the targeted re-run of a pytest node or a unittest case:
// the command through its runner word, the options it can carry, then
// the unit's id (positional paths dropped).
func pyRerun(cmd, runner, id string, switches map[string]bool) (string, string) {
	words, raws, simple := shellWordsRaw(cmd)
	i := tokenIndex(words, runner)
	if !simple || i < 0 {
		return "", "not re-runnable: the planned command is not a simple " + runner + " invocation"
	}
	_, ci := commandWord(words)
	if ci < 0 || ci > i {
		return "", "not re-runnable: the planned command is not a simple " + runner + " invocation"
	}
	parts := leadingAssignments(cmd)
	parts = append(parts, raws[ci:i+1]...)
	for j, w := range words[i+1:] {
		switch {
		case w == "discover":
			return "", "not re-runnable: `unittest discover` arguments cannot be carried to one case"
		case !strings.HasPrefix(w, "-"):
			// a path or an id: the re-run names its own
		case switches[w] || reAttachedOpt.MatchString(w):
			parts = append(parts, raws[i+1+j])
		default:
			return "", fmt.Sprintf("not re-runnable: the planned command's option %s may take a value the re-run cannot tell from a path", w)
		}
	}
	return strings.Join(append(parts, shellQuote(id)), " "), ""
}

// flagNames answers the flag words of a raw flag list (values dropped).
func flagNames(flags []string) []string {
	var out []string
	for _, f := range flags {
		if strings.HasPrefix(f, "-") {
			out = append(out, f)
		}
	}
	return out
}
