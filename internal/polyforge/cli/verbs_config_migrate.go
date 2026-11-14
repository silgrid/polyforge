package cli

// verbs_config_migrate.go: `pf config migrate` — the one
// transition step that moves each mapped workspace's retired
// `.polyforge.yaml` `summarize:` / `memory_policy:` sections into cli.toml
// (the [summarize] table and one [memory_policy."<path>".<entry>] block per
// path) and then deletes them from the yaml. One plan-then-apply flow, no
// daemon:
//
//   - plan (writes nothing): every mapped workspace's yaml is read through
//     workspace.LegacySections; a disagreement — summarize values that
//     differ between workspaces or from an existing [summarize] (effective
//     values: an absent optional key is its documented default), a path
//     whose existing cli.toml block differs from its yaml block, a section
//     whose value is invalid, or a yaml that would not load without the
//     sections — fails with one itemized error and no write. Identical
//     existing values are not a disagreement (nothing to write for them).
//   - apply: a sibling `<file>.bak-<UTC timestamp>` backup of every file
//     that changes (never overwriting one), then cli.toml atomically
//     first, then each yaml with exactly the sections' lines removed. An
//     interruption after the cli.toml write converges on the next run
//     (cli.toml already holds equal values — only the yaml deletion is
//     left); a machine with nothing to move prints `nothing to migrate`,
//     changes no byte and exits 0.
//
// Values move verbatim: the command hardcodes none and picks no side.
// There is no reverse command; rollback is stop the daemon → restore the
// backups → install the older pf (docs/upgrade.md). Reach: a local-CLI
// verb with `pf config set`'s reach (the operator's file permissions); it
// adds no daemon route. Deletion condition: it goes together with the
// legacy refusal (workspace/legacy.go) once every mapped workspace on the
// supported install base has migrated.

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
	"gopkg.in/yaml.v3"
)

// migrateNow is the backup timestamp clock (a test seam).
var migrateNow = time.Now

// migrateSummarize is one workspace's summarize section, as declared.
type migrateSummarize struct {
	source      string // "<yaml>: line N" or "cli.toml [summarize]"
	models      []string
	language    string // "" = absent
	intervalSec int    // 0 = absent
}

func (m migrateSummarize) effective() string {
	lang, iv := m.language, m.intervalSec
	if lang == "" {
		lang = workspace.DefaultSummarizeLanguage
	}
	if iv == 0 {
		iv = workspace.DefaultSummarizeIntervalSec
	}
	return fmt.Sprintf("models=[%s] language=%q interval_sec=%d", strings.Join(m.models, ", "), lang, iv)
}

// migrateYaml is one workspace yaml the migration changes.
type migrateYaml struct {
	path     string
	raw      []byte
	mode     fs.FileMode
	sections []workspace.LegacySection
}

// migratePolicy is one path's yaml memory_policy block, entry order kept.
type migratePolicy struct {
	ws, source string
	order      []string
	entries    map[string]workspace.PolicyEntry
}

func runConfigMigrate(ctx *Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("config migrate: takes no argument (it reads every mapped workspace of cli.toml)")
	}
	path := configPath(ctx)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config %s: %w", path, err)
	}
	cfg, err := ParseConfigBytes(path, raw)
	if err != nil {
		return err
	}

	// ---- plan: read and judge everything, write nothing.
	var issues []string
	var yamls []migrateYaml
	var sums []migrateSummarize
	var policies []migratePolicy
	for _, ws := range cfg.Mapping.Workspaces() {
		ypath := workspace.PathFor(ws)
		yraw, rerr := os.ReadFile(ypath)
		if errors.Is(rerr, fs.ErrNotExist) {
			continue
		}
		if rerr != nil {
			issues = append(issues, fmt.Sprintf("%s: unreadable: %v", ypath, rerr))
			continue
		}
		secs, perr := workspace.LegacySections(yraw)
		if perr != nil {
			issues = append(issues, fmt.Sprintf("%s: does not parse: %s", ypath, strings.ReplaceAll(perr.Error(), "\n", "; ")))
			continue
		}
		if len(secs) == 0 {
			continue
		}
		rest := workspace.RemoveLegacySections(yraw, secs)
		if _, lerr := loadWorkspaceBytes(string(rest), ws, cfg.Mapping.ProjectsOf(ws)); lerr != nil {
			issues = append(issues, fmt.Sprintf("%s: would not load without the moved sections: %s", ypath, lerr.Error()))
			continue
		}
		mode := os.FileMode(0o644)
		if m, ok := atomicfile.ReadMode(ypath); ok {
			mode = m
		}
		yamls = append(yamls, migrateYaml{path: ypath, raw: yraw, mode: mode, sections: secs})
		for _, s := range secs {
			source := fmt.Sprintf("%s: line %d", ypath, s.Line)
			switch s.Key {
			case workspace.SummarizeKey:
				m, reason := decodeLegacySummarize(s.Value)
				if reason != "" {
					issues = append(issues, source+": summarize: "+reason)
					continue
				}
				m.source = source
				sums = append(sums, m)
			case workspace.MemoryPolicyKey:
				p, reason := decodeLegacyPolicy(s.Value)
				if reason != "" {
					issues = append(issues, source+": memory_policy: "+reason)
					continue
				}
				p.ws, p.source = ws, source
				policies = append(policies, p)
			}
		}
	}

	// summarize: one machine-level table — every source must agree.
	var writeSum *migrateSummarize
	if len(sums) > 0 {
		all := append([]migrateSummarize{}, sums...)
		if cfg.Summarize != nil {
			all = append(all, migrateSummarize{source: path + " [summarize]", models: cfg.Summarize.Models, language: cfg.Summarize.Language, intervalSec: cfg.Summarize.IntervalSec})
		}
		agree := true
		for _, s := range all[1:] {
			if s.effective() != all[0].effective() {
				agree = false
			}
		}
		if !agree {
			parts := make([]string, 0, len(all))
			for _, s := range all {
				parts = append(parts, s.source+": "+s.effective())
			}
			issues = append(issues, "the summarize values disagree (one machine-level [summarize] table; align the sources, then re-run): "+strings.Join(parts, "; "))
		} else if cfg.Summarize == nil {
			m := sums[0]
			for _, s := range sums[1:] { // keep a declared optional key verbatim
				if m.language == "" {
					m.language = s.language
				}
				if m.intervalSec == 0 {
					m.intervalSec = s.intervalSec
				}
			}
			writeSum = &m
		}
	}

	// memory_policy: one block per path; an existing block must be equal.
	var writePolicies []migratePolicy
	for _, p := range policies {
		existing := cfg.MemoryPolicy[p.ws]
		if len(existing) == 0 {
			writePolicies = append(writePolicies, p)
			continue
		}
		if reflect.DeepEqual(existing, p.entries) {
			continue
		}
		names := map[string]bool{}
		for k := range existing {
			names[k] = true
		}
		for k := range p.entries {
			names[k] = true
		}
		var differ []string
		for k := range names {
			if !reflect.DeepEqual(existing[k], p.entries[k]) {
				differ = append(differ, k)
			}
		}
		sort.Strings(differ)
		issues = append(issues, fmt.Sprintf("[memory_policy.%q] in %s differs from %s for entr(ies) %s (reconcile them, then re-run)", p.ws, path, p.source, strings.Join(differ, ", ")))
	}

	var proposed []byte
	if len(issues) == 0 && (writeSum != nil || len(writePolicies) > 0) {
		c := newConfigLines(string(raw))
		if writeSum != nil {
			c.setSummarize("models", "["+quoteList(writeSum.models)+"]")
			if writeSum.language != "" {
				c.setSummarize("language", quoteTOML(writeSum.language))
			}
			if writeSum.intervalSec != 0 {
				c.setSummarize("interval_sec", fmt.Sprint(writeSum.intervalSec))
			}
		}
		for _, p := range writePolicies {
			for _, entry := range p.order {
				c.appendBlock(policyBlockLines(p.ws, entry, p.entries[entry])...)
			}
		}
		proposed = []byte(c.String())
		if _, perr := ParseConfigBytes(path, proposed); perr != nil {
			issues = append(issues, "the moved values would not load in cli.toml: "+perr.Error())
		}
	}
	if len(issues) > 0 {
		return fmt.Errorf("config migrate: refused, nothing was written:\n  - %s", strings.Join(issues, "\n  - "))
	}
	if proposed == nil && len(yamls) == 0 {
		if ctx.JSON {
			return emitJSON(ctx, map[string]any{"result": "nothing to migrate"})
		}
		fmt.Fprintln(ctx.Stdout, "nothing to migrate")
		return nil
	}

	// ---- apply: backups, then cli.toml first, then the yamls.
	stamp := migrateNow().UTC().Format("20060102T150405Z")
	type change struct{ File, Backup, What string }
	var changes []change
	if proposed != nil {
		bak, err := writeBackup(path, raw, stamp)
		if err != nil {
			return err
		}
		if err := writeMigratedConfig(path, raw, proposed); err != nil {
			return err
		}
		changes = append(changes, change{path, bak, "the moved summarize/memory_policy values written"})
	}
	for _, y := range yamls {
		bak, err := writeBackup(y.path, y.raw, stamp)
		if err != nil {
			return err
		}
		if err := writeMigratedYaml(y); err != nil {
			return err
		}
		keys := make([]string, 0, len(y.sections))
		for _, s := range y.sections {
			keys = append(keys, s.Key+":")
		}
		changes = append(changes, change{y.path, bak, strings.Join(keys, " and ") + " removed"})
	}
	if ctx.JSON {
		return emitJSON(ctx, map[string]any{"result": "migrated", "changes": changes})
	}
	for _, ch := range changes {
		fmt.Fprintf(ctx.Stdout, "%s: %s (backup %s)\n", ch.File, ch.What, ch.Backup)
	}
	fmt.Fprintln(ctx.Stdout, "migrated: restart the daemon (or run `pf config reload`) so it serves the moved values")
	return nil
}

// decodeLegacySummarize reads a retired summarize section's value with
// the cli.toml table's rules (models mandatory and canonical, language
// free single-line text, interval_sec in range).
func decodeLegacySummarize(n *yaml.Node) (migrateSummarize, string) {
	var v struct {
		Models      []string `yaml:"models"`
		Language    *string  `yaml:"language"`
		IntervalSec *int     `yaml:"interval_sec"`
	}
	if reason := decodeStrict(n, &v); reason != "" {
		return migrateSummarize{}, reason + " (the keys are models, language, interval_sec)"
	}
	if len(v.Models) == 0 {
		return migrateSummarize{}, "models is absent or empty — the cli.toml table needs at least one candidate (remove the section by hand to keep the lane off)"
	}
	models, reason := normalizeSummarizeModels(v.Models)
	if reason != "" {
		return migrateSummarize{}, "models " + reason
	}
	m := migrateSummarize{models: models}
	if v.Language != nil {
		lang, reason := checkLanguage(*v.Language)
		if reason != "" {
			return m, "language " + reason
		}
		m.language = lang
	}
	if v.IntervalSec != nil {
		n, reason := parseIntervalValue(fmt.Sprint(*v.IntervalSec))
		if reason != "" {
			return m, "interval_sec " + reason
		}
		m.intervalSec = n
	}
	return m, ""
}

// decodeLegacyPolicy reads a retired memory_policy section's value: entry
// (in file order) → record/skip lists, judged like the cli.toml family.
func decodeLegacyPolicy(n *yaml.Node) (migratePolicy, string) {
	p := migratePolicy{entries: map[string]workspace.PolicyEntry{}}
	if n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return p, "must be a non-empty mapping of `default` / project → { record: [...], skip: [...] }"
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		name := n.Content[i].Value
		var v struct {
			Record *[]string `yaml:"record"`
			Skip   *[]string `yaml:"skip"`
		}
		if reason := decodeStrict(n.Content[i+1], &v); reason != "" {
			return p, name + ": " + reason + " (an entry's keys are record, skip)"
		}
		if v.Record == nil && v.Skip == nil {
			return p, name + ": the entry declares no list"
		}
		var e workspace.PolicyEntry
		for _, l := range []struct {
			src *[]string
			dst *[]string
			key string
		}{{v.Record, &e.Record, "record"}, {v.Skip, &e.Skip, "skip"}} {
			if l.src == nil {
				continue
			}
			list, reason := checkPolicyList(*l.src)
			if reason != "" {
				return p, name + "." + l.key + ": " + reason
			}
			*l.dst = list
		}
		if _, dup := p.entries[name]; dup {
			return p, name + ": duplicate entry"
		}
		p.order = append(p.order, name)
		p.entries[name] = e
	}
	return p, ""
}

// decodeStrict decodes one yaml node into v refusing unknown keys.
func decodeStrict(n *yaml.Node, v any) string {
	raw, err := yaml.Marshal(n)
	if err != nil {
		return err.Error()
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return strings.ReplaceAll(err.Error(), "\n", "; ")
	}
	return ""
}

// quoteTOML renders a cli.toml string value (the parser refuses escapes,
// so a value carrying a quote or backslash is refused at re-parse).
func quoteTOML(s string) string { return `"` + s + `"` }

// writeBackup writes the pre-change bytes beside file as
// `<file>.bak-<stamp>` (the next free `-<n>` suffix when that exists),
// never overwriting a backup. It answers the backup path.
func writeBackup(file string, raw []byte, stamp string) (string, error) {
	mode := os.FileMode(0o600)
	if m, ok := atomicfile.ReadMode(file); ok {
		mode = m
	}
	base := file + ".bak-" + stamp
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("config migrate: backup %s: %w", name, err)
		}
		_, werr := f.Write(raw)
		serr := f.Sync()
		cerr := f.Close()
		if err := errors.Join(werr, serr, cerr); err != nil {
			return "", fmt.Errorf("config migrate: backup %s: %w", name, err)
		}
		return name, nil
	}
}

// writeMigratedConfig lands the proposed cli.toml under the writer flock,
// refusing when the file changed since the plan read it.
func writeMigratedConfig(path string, planned, proposed []byte) error {
	lock, err := lockConfigFile(path)
	if err != nil {
		return fmt.Errorf("config: lock %s: %w", path, err)
	}
	defer unlockConfigFile(lock)
	cur, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config %s: %w", path, err)
	}
	if !bytes.Equal(cur, planned) {
		return fmt.Errorf("config migrate: %s changed while the migration was planned — nothing was written to it; re-run", path)
	}
	if err := writeConfigAtomic(path, proposed); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// writeMigratedYaml removes the moved sections from one yaml under its
// writer flock, refusing when the file changed since the plan read it.
func writeMigratedYaml(y migrateYaml) error {
	lock, err := lockConfigFile(y.path)
	if err != nil {
		return fmt.Errorf("workspace: lock %s: %w", y.path, err)
	}
	defer unlockConfigFile(lock)
	cur, err := os.ReadFile(y.path)
	if err != nil {
		return fmt.Errorf("workspace %s: %w", y.path, err)
	}
	if !bytes.Equal(cur, y.raw) {
		return fmt.Errorf("config migrate: %s changed while the migration was planned — cli.toml already holds the moved values; re-run to finish", y.path)
	}
	if err := atomicfile.Write(y.path, workspace.RemoveLegacySections(y.raw, y.sections), y.mode); err != nil && !errors.Is(err, atomicfile.ErrDirSync) {
		return fmt.Errorf("workspace: write %s: %w", y.path, err)
	}
	return nil
}
