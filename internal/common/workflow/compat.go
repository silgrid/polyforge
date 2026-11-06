package workflow

// compat.go: the contract-version rule's mechanical half. ContractVersion
// moves only on an incompatible change, at most once per release
// (contract.go). The registry dump (RegistryDump) is compared with its
// baseline (testdata/contract.baseline.golden: the shape the last bump, or
// the last compatible change, left); a difference the dump shows
// mechanically — a key gone from a key set, a key now required, a value
// gone from a key's closed set — is incompatible and must be registered in
// contract-pending.md, which the release that follows reads to bump the
// version once. Every other change is compatible: the golden file and the
// baseline are regenerated in place, the version stays.

import (
	"fmt"
	"sort"
	"strings"
)

// PendingFile is the list of incompatible changes registered since the
// last bump, beside this package's sources: one `## <what changed>`
// heading per entry, its body what the migration takes.
const PendingFile = "contract-pending.md"

// dumpShape is what a registry dump shows mechanically: its first line,
// every key as `<set>.<key>`, every required key and every closed value.
type dumpShape struct {
	head                  string
	keys, required, value map[string]bool
}

// parseDump reads a dump's shape: the `[<set> keys]` sections (not
// `[removed keys]`), `[required]` and `[values]`.
func parseDump(dump string) dumpShape {
	s := dumpShape{keys: map[string]bool{}, required: map[string]bool{}, value: map[string]bool{}}
	lines := strings.Split(dump, "\n")
	if len(lines) > 0 {
		s.head = lines[0]
	}
	section := ""
	for _, l := range lines[1:] {
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(l, "["), "]")
			continue
		}
		if l == "" {
			continue
		}
		switch {
		case section == "required":
			s.required[l] = true
		case section == "values":
			s.value[l] = true
		case section != "removed keys" && strings.HasSuffix(section, " keys"):
			if k, _, ok := strings.Cut(l, ": "); ok {
				s.keys[strings.TrimSuffix(section, " keys")+"."+k] = true
			}
		}
	}
	return s
}

// incompatibleChanges answers the differences of cur from base no lane
// valid under base survives: a removed key, a new required key, a removed
// value. Two dumps of different contract versions are not compared (a
// bump rewrites the baseline).
func incompatibleChanges(base, cur string) []string {
	b, c := parseDump(base), parseDump(cur)
	if b.head != c.head {
		return nil
	}
	var out []string
	for k := range b.keys {
		if !c.keys[k] {
			out = append(out, "removed key "+k)
		}
	}
	for k := range c.required {
		if !b.required[k] {
			out = append(out, "new required key "+k)
		}
	}
	for v := range b.value {
		if !c.value[v] {
			out = append(out, "removed value "+v)
		}
	}
	sort.Strings(out)
	return out
}

// judgeRegistry is the pin's verdict on the registry dump, its golden
// file, the baseline and the number of registered incompatible changes:
// an unregistered incompatible change, then a golden file the dump moved
// away from, then (nothing registered) a baseline other than the golden
// file.
func judgeRegistry(dump, golden, baseline string, pending int) error {
	if diffs := incompatibleChanges(baseline, dump); len(diffs) > 0 && pending == 0 {
		return fmt.Errorf("incompatible contract change (%s): register it in internal/common/workflow/%s — one `## <what changed>` entry saying what the migration takes (user lanes, work items in flight, the deployment); ContractVersion is bumped once at the release, leave it at %d now",
			strings.Join(diffs, "; "), PendingFile, ContractVersion)
	}
	if dump != golden {
		return fmt.Errorf("the contract registry changed: regenerate the golden file and its baseline in place (PF_WRITE_CONTRACT_GOLDEN=1) and the lane schema (go generate ./internal/common/workflow); ContractVersion stays %d\n%s",
			ContractVersion, lineDiff(golden, dump))
	}
	if pending == 0 && baseline != golden {
		return fmt.Errorf("the registry baseline is not the golden file while %s registers nothing: rewrite it in place (PF_WRITE_CONTRACT_GOLDEN=1); ContractVersion stays %d\n%s",
			PendingFile, ContractVersion, lineDiff(baseline, golden))
	}
	return nil
}

// lineDiff lists the lines only in was (`-`) and only in now (`+`).
func lineDiff(was, now string) string {
	in := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(s, "\n") {
			m[l] = true
		}
		return m
	}
	w, n := in(was), in(now)
	var b strings.Builder
	for _, l := range strings.Split(was, "\n") {
		if !n[l] {
			b.WriteString("- " + l + "\n")
		}
	}
	for _, l := range strings.Split(now, "\n") {
		if !w[l] {
			b.WriteString("+ " + l + "\n")
		}
	}
	return b.String()
}

// pendingEntries answers the entries of contract-pending.md: each line
// that starts `## ` (the release reads the list by the same rule).
func pendingEntries(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "## ") {
			out = append(out, strings.TrimPrefix(l, "## "))
		}
	}
	return out
}
