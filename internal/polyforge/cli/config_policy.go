package cli

// config_policy.go: the cli.toml text of the memory_policy family that pf
// itself writes `pf init`'s editable copy of the built-in
// default and `pf config migrate`'s moved blocks — and the locked,
// validated, atomic insert they share with `pf config set`.

import (
	"fmt"
	"os"
	"strconv"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// policyBlockComment is the comment `pf init` writes above the default
// block: what the lists mean and how a project entry extends them.
var policyBlockComment = []string{
	"# memory_policy: what the workflow's last step puts into the work item's",
	"# experience.wrap summary memory for this workspace. record = worth",
	"# remembering, skip = never in it. A project entry's lists are appended",
	"# to default's: [memory_policy.\"<path>\".<project>] record = [...] skip = [...]",
}

// policyBlockLines renders one entry table: its header and its declared
// lists, one item per line (a multi-line array keeps every item whole).
func policyBlockLines(path, entry string, e workspace.PolicyEntry) []string {
	out := []string{policyHeader(path, entry)}
	for _, l := range []struct {
		name  string
		items []string
	}{{"record", e.Record}, {"skip", e.Skip}} {
		if l.items == nil {
			continue
		}
		if len(l.items) == 0 {
			out = append(out, l.name+" = []")
			continue
		}
		out = append(out, l.name+" = [")
		for _, item := range l.items {
			out = append(out, "  "+strconv.Quote(item)+",")
		}
		out = append(out, "]")
	}
	return out
}

// defaultPolicyBlock is `pf init`'s block for path: the comment and the
// built-in default lists.
func defaultPolicyBlock(path string) []string {
	builtin := workspace.DefaultMemoryPolicy()
	return append(append([]string{}, policyBlockComment...),
		policyBlockLines(path, workspace.DefaultKey, workspace.PolicyEntry{Record: builtin.Record, Skip: builtin.Skip})...)
}

// initMemoryPolicy writes the built-in default block for ws into the
// cli.toml at path once: ws must be a mapped workspace ("unmapped": nothing
// written), and a path already declaring any entry is left alone
// ("unchanged"). The insert runs under the writer flock, re-parses the
// proposed bytes through the one validator and lands atomically
// ("inserted").
func initMemoryPolicy(path, ws string) (string, error) {
	lock, err := lockConfigFile(path)
	if err != nil {
		return "", fmt.Errorf("config: lock %s: %w", path, err)
	}
	defer unlockConfigFile(lock)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("config %s: %w", path, err)
	}
	cfg, err := ParseConfigBytes(path, raw)
	if err != nil {
		return "", err
	}
	if cfg.Mapping.ProjectsOf(ws) == nil {
		return "unmapped", nil
	}
	if len(cfg.MemoryPolicy[ws]) > 0 {
		return "unchanged", nil
	}
	c := newConfigLines(string(raw))
	c.appendBlock(defaultPolicyBlock(ws)...)
	proposed := []byte(c.String())
	if _, err := ParseConfigBytes(path, proposed); err != nil {
		return "", fmt.Errorf("the default memory_policy block would not load: %w", err)
	}
	if err := writeConfigAtomic(path, proposed); err != nil {
		return "", fmt.Errorf("config: write %s: %w", path, err)
	}
	return "inserted", nil
}
