package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctor_scope_gates.go: the read-only readiness probe behind `pf doctor`'s
// "scoped-gates readiness" row. The verification-tier mechanism is
// platform-wide: the release tree ships the engine `tools/verify-scope`, and a
// repository may narrow its scope or declare its bar with a declarative
// `.polyforge/verify-scope.conf`. This row makes readiness visible: for every
// mapped project's registered repos it reads (never executes) the clone's
// manifests and conf and the release tree's engine, and answers whether the
// engine default or the repo conf is effective. A repo with no local clone is
// `unknown`, never faked green.
//
// The verdict per cloned repo (the engine is conf-first: it reports a repo
// conf's commands for every stack):
//   - a `stack=go|npm|python|rust` pin, or a single supported manifest (go.mod,
//     package.json, pyproject.toml/requirements.txt, Cargo.toml): the analyzer
//     is effective — scoped-ready, unless the carries layer would force every
//     scoped ticket to full: an npm/python/rust repo with an undeclared suite
//     (no carries: header) or an empty/malformed carries conf key warns,
//     naming the suites; a repo whose suites all declare is named clean;
//   - no analyzer — a `stack=none` pin, or neither manifest — with a conf
//     declaring at least one command (fast_gate=, full_command=, check=):
//     the repo conf is effective, on the full tier only (no analyzer, no
//     scoped tier) — pass;
//   - no analyzer and no conf command: warn, naming the consequence (the
//     verification step derives the bar ad hoc on every run, there is no
//     scoped tier, and a future runner dispatch fails closed);
//   - two or more supported manifests without a pin: warn (unpinned
//     multi-manifest);
//   - any effective verdict without the release-tree engine: warn.
//
// The row is informational and additive: it is registered with prerequisite
// needMapping (NOT needAihub, so the locally decidable half still fires when
// aihub is down), it never returns `fail` (an unsupported stack or a missing
// prerequisite is a capability gap, not an illness — it cannot move `ok` or
// the exit code), and `--harness` skips doctorChecks wholesale. One authority
// is cited, never restated: docs/contributing.md's "Verification tiers"
// section (the same section the filing skill names).

// docChScopedGates is the row name (sibling convention: one const beside
// the layer it implements).
const docChScopedGates = "scoped-gates readiness"

// scopedGatesEngine is the release-shipped engine path, relative to the
// release checkout (<data-dir>/release/current/tools/verify-scope).
const scopedGatesEngine = "tools/verify-scope"

// scopedGatesConf is the declarative repository override, relative to the
// clone root.
const scopedGatesConf = ".polyforge/verify-scope.conf"

// scopedGatesPointer is the one shared authority both this row and the
// filing skill name (the ticket's 不另造权威): the repo's own
// verification-tier section.
const scopedGatesPointer = "docs/contributing.md『Verification tiers』"

// scopedGatesCheckName and scopedGatesReservedName are the engine's check=
// name rules: letters, digits, '.', '_' and '-', starting with a letter or
// digit, and never the anonymous/analyzer form <phase>-<n>.
var (
	scopedGatesCheckName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	scopedGatesReservedName = regexp.MustCompile(`^(fast|full|scoped)-[0-9]`)
)

// scopedGatesConfFacts is what the row reads from a conf: the first valid
// stack pin, the command count, and the carries layer's keys.
type scopedGatesConfFacts struct {
	pin       string
	commands  int
	runner    string   // e2e_runner=
	suiteGlob []string // suite_glob=
	maps      []string // e2e_map=<glob>=<suite>
	excludes  []string // exclude=
	smoke     []string // smoke_suite= (the full-tier e2e selection opt-in), unique, file order
	rejected  []string // carries key lines the engine rejects (it then always reports full)
}

// scopedGatesReadConf reads a conf's first valid `stack=` pin (go, npm,
// python, rust or none; an invalid pin is ignored, as in the engine), counts
// the commands it declares — fast_gate=, full_command= and well-formed check=
// lines, as the engine accepts them: a value that is empty or begins with a
// command substitution is no command — and reads the carries keys
// (e2e_runner=, suite_glob=, e2e_map=, smoke_suite=) and exclude= with the
// engine's fail-closed rules. The file is only read, never evaluated; a missing or
// unreadable file answers no pin and no command.
func scopedGatesReadConf(path string) scopedGatesConfFacts {
	var c scopedGatesConfFacts
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()
	reportable := func(cmd string) bool {
		return cmd != "" && !strings.HasPrefix(cmd, "$(") && !strings.HasPrefix(cmd, "`")
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimLeft(sc.Text(), " \t\r\v\f")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "stack":
			v := strings.TrimSpace(val)
			if c.pin == "" && (v == "go" || v == "npm" || v == "python" || v == "rust" || v == "none") {
				c.pin = v
			}
			// An invalid pin is ignored; the first valid pin wins, as in the engine.
		case "fast_gate", "full_command":
			if reportable(val) {
				c.commands++
			}
		case "check":
			parts := strings.SplitN(val, "|", 3)
			if len(parts) == 3 && scopedGatesCheckName.MatchString(parts[0]) && !scopedGatesReservedName.MatchString(parts[0]) &&
				(parts[1] == "fast" || parts[1] == "full") && reportable(parts[2]) {
				c.commands++
			}
		case "exclude":
			if val != "" {
				c.excludes = append(c.excludes, val)
			}
		case "e2e_runner":
			v := strings.TrimRight(val, " \t\r\v\f")
			if !reportable(v) || c.runner != "" {
				c.rejected = append(c.rejected, line)
				continue
			}
			c.runner = v
		case "suite_glob":
			v := strings.TrimRight(val, " \t\r\v\f")
			if v == "" {
				c.rejected = append(c.rejected, line)
				continue
			}
			c.suiteGlob = append(c.suiteGlob, v)
		case "e2e_map":
			v := strings.TrimRight(val, " \t\r\v\f")
			g, suite, ok := strings.Cut(v, "=")
			if !ok || g == "" || suite == "" || strings.Contains(suite, "=") {
				c.rejected = append(c.rejected, line)
				continue
			}
			c.maps = append(c.maps, v)
		case "smoke_suite":
			v := strings.TrimRight(val, " \t\r\v\f")
			if !scopedGatesSmokePath(v) {
				c.rejected = append(c.rejected, line)
				continue
			}
			if !slices.Contains(c.smoke, v) {
				c.smoke = append(c.smoke, v)
			}
		}
	}
	return c
}

// scopedGatesSmokePath reports whether a smoke_suite= value is one
// repository-relative path, as the engine accepts it: non-empty, no blank,
// not absolute, no leading ./ or ../ and no /../ segment.
func scopedGatesSmokePath(v string) bool {
	return v != "" && !strings.ContainsAny(v, " \t\r\n\v\f") && !strings.HasPrefix(v, "/") &&
		!strings.HasPrefix(v, "./") && !strings.HasPrefix(v, "../") && !strings.Contains(v, "/../")
}

// scopedGatesSmokeMissing answers the declared smoke suites that are not
// discovered suites: the engine's full-tier e2e selection then falls back
// to the full tail (the scoped tier ignores smoke_suite=).
func scopedGatesSmokeMissing(suites []string, conf scopedGatesConfFacts) []string {
	var missing []string
	for _, s := range conf.smoke {
		if !slices.Contains(suites, s) {
			missing = append(missing, s)
		}
	}
	return missing
}

// scopedGatesNoAnalyzer is the consequence a repo with no analyzer and no
// conf command carries; the warn detail spells it out.
const scopedGatesNoAnalyzer = "the verification step derives the bar ad hoc on every run, there is no scoped tier (a scoped ticket falls back to full), and a future runner dispatch fails closed; declare the bar in " + scopedGatesConf + " (fast_gate=, full_command= or check= lines; stack=none pins a conf-only repo)"

// scopedGatesFileExists is a presence read (never an execution).
func scopedGatesFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// scopedGatesCheck is the doctor row body: for every mapped project's
// registered repos, read the local clone's manifests and conf and the release
// tree's engine and report the readiness. The registered-repo set is read from
// aihub's project record (the member-readable GET /api/v1/projects/{id}), one
// read per mapped project; a project whose read fails contributes no repos and
// its reason is surfaced — the row never invents a pass over an unread set.
// Status pass|warn|skip, never fail.
func scopedGatesCheck(r *doctorRun) (string, string) {
	aihub := aihubclient.New(r.cfg.Server, r.cfg.APIKey)
	var problems, unknown, readFails, confOnly, carriesClean []string
	ready, registered := 0, 0
	home, _ := os.UserHomeDir()
	enginePath := filepath.Join(releaseCurrent(home), scopedGatesEngine)
	enginePresent := scopedGatesFileExists(enginePath)
	for _, project := range r.cfg.Mapping.Projects() {
		ws, err := r.cfg.Mapping.WorkspaceFor(project)
		if err != nil {
			continue // the mapping enumeration guarantees a workspace
		}
		raw, err := aihub.GetProject(project)
		if err != nil {
			readFails = append(readFails, fmt.Sprintf("project %q: %v", project, err))
			continue
		}
		var proj api.Project
		if err := json.Unmarshal(raw, &proj); err != nil {
			readFails = append(readFails, fmt.Sprintf("project %q: %v", project, err))
			continue
		}
		for _, repo := range proj.Repos {
			if repo.Name == "" {
				continue
			}
			registered++
			clone := workspace.RepoCloneDir(ws, repo.Name)
			if !workspace.IsRepoClone(clone) {
				unknown = append(unknown, fmt.Sprintf("%s/%s: no local clone under %s — readiness unknown", project, repo.Name, clone))
				continue
			}
			confPath := filepath.Join(clone, workspace.GeneratedDir, "verify-scope.conf")
			manifests, stacks := scopedGatesManifests(clone)
			conf := scopedGatesReadConf(confPath)
			pin, commands := conf.pin, conf.commands
			noAnalyzer := false
			analyzer := pin
			switch {
			case pin == "none" && commands == 0:
				problems = append(problems, fmt.Sprintf("%s/%s: %s pins stack=none but declares no command — %s (%s)", project, repo.Name, scopedGatesConf, scopedGatesNoAnalyzer, scopedGatesPointer))
				continue
			case pin == "none":
				// the conf is the whole bar: no analyzer, full tier only.
				noAnalyzer = true
			case pin != "":
				// the conf pins the analyzer, so the repo's own declaration is effective.
			case len(stacks) > 1:
				problems = append(problems, fmt.Sprintf("%s/%s: %s are present without a stack= pin — an unpinned multi-manifest repo; a scoped ticket falls back to full (%s)", project, repo.Name, strings.Join(manifests, ", "), scopedGatesPointer))
				continue
			case len(stacks) == 0 && commands == 0:
				problems = append(problems, fmt.Sprintf("%s/%s: no supported stack manifest (go.mod, package.json, pyproject.toml/requirements.txt or Cargo.toml) and no repo conf command — %s (%s)", project, repo.Name, scopedGatesNoAnalyzer, scopedGatesPointer))
				continue
			case len(stacks) == 0:
				// another stack whose conf declares its bar: conf-first.
				noAnalyzer = true
			default:
				analyzer = stacks[0]
			}
			// A conf names declarations the engine executes; without the
			// release-tree engine it does nothing, so the repo is not ready.
			if !enginePresent {
				problems = append(problems, fmt.Sprintf("%s/%s: the release tree lacks %s — a scoped ticket falls back to full (%s)", project, repo.Name, enginePath, scopedGatesPointer))
				continue
			}
			if noAnalyzer {
				confOnly = append(confOnly, project+"/"+repo.Name)
				continue
			}
			// The carries layer (npm, python, rust): an empty or malformed
			// carries key, or one undeclared suite, makes every report full.
			if analyzer == "npm" || analyzer == "python" || analyzer == "rust" {
				if len(conf.rejected) > 0 {
					problems = append(problems, fmt.Sprintf("%s/%s: %s carries an empty or malformed carries key (%s) — every report falls back to full (%s)", project, repo.Name, scopedGatesConf, strings.Join(capRows(conf.rejected), "; "), scopedGatesPointer))
					continue
				}
				suites, undeclared := scopedGatesCarries(clone, analyzer, conf)
				if len(undeclared) > 0 {
					problems = append(problems, fmt.Sprintf("%s/%s: %d undeclared suite(s) without a carries: header in the opening comment block — a scoped ticket falls back to full: %s (%s)", project, repo.Name, len(undeclared), strings.Join(capRows(undeclared), ", "), scopedGatesPointer))
					continue
				}
				if missing := scopedGatesSmokeMissing(suites, conf); len(missing) > 0 {
					problems = append(problems, fmt.Sprintf("%s/%s: %s declares smoke_suite= naming no discovered suite (%s) — the full-tier e2e selection falls back to the full e2e command (%s)", project, repo.Name, scopedGatesConf, strings.Join(capRows(missing), ", "), scopedGatesPointer))
					continue
				}
				clean := fmt.Sprintf("%s/%s (%d suite(s))", project, repo.Name, len(suites))
				if len(conf.smoke) > 0 {
					clean = fmt.Sprintf("%s/%s (%d suite(s); full-tier e2e selection opted in, smoke: %s)", project, repo.Name, len(suites), strings.Join(capRows(conf.smoke), ", "))
				}
				carriesClean = append(carriesClean, clean)
			}
			ready++
		}
	}
	// carriesNote names the carries-layer repos with no undeclared suite.
	carriesNote := ""
	if len(carriesClean) > 0 {
		carriesNote = fmt.Sprintf("; carries clean (no undeclared suite): %s", strings.Join(capRows(carriesClean), "; "))
	}
	// confNote names the repos whose conf is effective with no analyzer: they
	// are verified, on the full tier only.
	confNote := ""
	if len(confOnly) > 0 {
		confNote = fmt.Sprintf("; %d repo(s) with no analyzer: repo conf effective (its commands, full tier only — no scoped tier): %s", len(confOnly), strings.Join(capRows(confOnly), "; "))
	}
	switch {
	case len(problems) > 0:
		detail := fmt.Sprintf("%d of %d registered repo(s) are not scoped-ready: %s", len(problems), registered, strings.Join(capRows(problems), "; "))
		detail += confNote + carriesNote
		if len(unknown) > 0 {
			detail += fmt.Sprintf("; %d repo(s) unknown (no local clone): %s", len(unknown), strings.Join(capRows(unknown), "; "))
		}
		if len(readFails) > 0 {
			detail += "; the registered-repo set is incomplete: " + strings.Join(capRows(readFails), "; ")
		}
		return "warn", detail
	case len(readFails) > 0:
		return "skip", fmt.Sprintf("scoped-gate readiness not decidable: the registered-repo set is incomplete: %s", strings.Join(capRows(readFails), "; "))
	default:
		detail := fmt.Sprintf("%d of %d registered repo(s) are scoped-ready (%s or %s)", ready, registered, scopedGatesEngine, scopedGatesConf)
		detail += confNote + carriesNote
		if len(unknown) > 0 {
			detail += fmt.Sprintf("; %d repo(s) unknown (no local clone): %s", len(unknown), strings.Join(capRows(unknown), "; "))
		}
		return "pass", detail
	}
}

// scopedGatesManifests answers the supported stack manifests a clone holds
// and the analyzers they select, in the engine's detection order.
func scopedGatesManifests(clone string) ([]string, []string) {
	var names, stacks []string
	add := func(name, stack string) {
		names = append(names, name)
		stacks = append(stacks, stack)
	}
	if scopedGatesFileExists(filepath.Join(clone, "go.mod")) {
		add("go.mod", "go")
	}
	if scopedGatesFileExists(filepath.Join(clone, "package.json")) {
		add("package.json", "npm")
	}
	if scopedGatesFileExists(filepath.Join(clone, "pyproject.toml")) || scopedGatesFileExists(filepath.Join(clone, "requirements.txt")) {
		add("pyproject.toml/requirements.txt", "python")
	}
	if scopedGatesFileExists(filepath.Join(clone, "Cargo.toml")) {
		add("Cargo.toml", "rust")
	}
	return names, stacks
}

// scopedGatesGlob matches a path against a shell-case glob, the engine's
// syntax for carries:, suite_glob= and exclude= (`*` crosses `/`).
func scopedGatesGlob(glob, path string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch ch := glob[i]; ch {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '\\':
			if i+1 < len(glob) {
				i++
				b.WriteString(regexp.QuoteMeta(string(glob[i])))
			}
		case '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(path)
}

// scopedGatesJSImport and scopedGatesRustMod are the engine's import scans
// for the npm and rust analyzers.
var (
	scopedGatesJSImport = regexp.MustCompile(`(?:from|import|require)\s*\(?\s*['"](\.\.?/[^'"]+)['"]`)
	scopedGatesRustMod  = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+([A-Za-z_][A-Za-z0-9_]*)\s*;`)
	scopedGatesPyIdent  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)
	scopedGatesPyFrom   = regexp.MustCompile(`^\s*from\s+([.A-Za-z0-9_]+)\s+import\s+(.*)$`)
	scopedGatesPyImport = regexp.MustCompile(`^\s*import\s+(.*)$`)
)

// scopedGatesCarries enumerates a clone's suites the way the engine does —
// the analyzer's default discovery or the conf's suite_glob= globs, minus
// exclude= matches and the e2e_runner= target, minus the undeclared modules
// another module imports (helpers) — and answers the suites and those whose
// opening comment block holds no valid carries: declaration (an e2e_map=
// line declares its suite outright; one naming no discovered suite is
// named too, as the engine refuses it). It reads files only.
func scopedGatesCarries(clone, analyzer string, conf scopedGatesConfFacts) ([]string, []string) {
	exists := func(rel string) bool {
		st, err := os.Stat(filepath.Join(clone, filepath.FromSlash(rel)))
		return err == nil && st.Mode().IsRegular()
	}
	var found []string
	if len(conf.suiteGlob) > 0 {
		_ = filepath.WalkDir(clone, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(clone, p)
			rel = filepath.ToSlash(rel)
			for _, g := range conf.suiteGlob {
				if scopedGatesGlob(g, rel) {
					found = append(found, rel)
					break
				}
			}
			return nil
		})
	} else {
		top := func(dir, suffix string) {
			entries, _ := os.ReadDir(filepath.Join(clone, dir))
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
					found = append(found, dir+"/"+e.Name())
				}
			}
		}
		switch analyzer {
		case "npm":
			top("e2e", ".mjs")
		case "rust":
			top("tests", ".rs")
		case "python":
			for _, dir := range []string{"e2e", "tests/e2e"} {
				_ = filepath.WalkDir(filepath.Join(clone, dir), func(p string, d os.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return nil
					}
					n := d.Name()
					if strings.HasSuffix(n, ".py") && (strings.HasPrefix(n, "test_") || strings.HasSuffix(n, "_test.py")) {
						rel, _ := filepath.Rel(clone, p)
						found = append(found, filepath.ToSlash(rel))
					}
					return nil
				})
			}
		}
	}

	// The e2e_runner= target files (through the npm script for `npm run <s>`).
	runners := map[string]bool{}
	if conf.runner != "" {
		words := strings.Fields(conf.runner)
		if len(words) >= 3 && words[0] == "npm" && words[1] == "run" {
			var pkg struct {
				Scripts map[string]any `json:"scripts"`
			}
			if raw, err := os.ReadFile(filepath.Join(clone, "package.json")); err == nil && json.Unmarshal(raw, &pkg) == nil {
				if cmd, ok := pkg.Scripts[words[2]].(string); ok {
					words = append(words, strings.Fields(cmd)...)
				}
			}
		}
		for _, w := range words {
			if w = strings.TrimPrefix(w, "./"); w != "" && exists(w) {
				runners[w] = true
			}
		}
	}
	excluded := func(rel string) bool {
		for _, g := range conf.excludes {
			if scopedGatesGlob(g, rel) || scopedGatesGlob(g, pathpkg.Base(rel)) {
				return true
			}
		}
		return false
	}
	var cands []string
	isCand := map[string]bool{}
	for _, f := range found {
		if excluded(f) || runners[f] || isCand[f] {
			continue
		}
		isCand[f] = true
		cands = append(cands, f)
	}
	sort.Strings(cands)

	// Every module another module of the graph imports.
	imported := map[string]bool{}
	seen := map[string]bool{}
	queue := append([]string(nil), cands...)
	for _, f := range queue {
		seen[f] = true
	}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		for _, t := range scopedGatesImports(clone, analyzer, f, exists) {
			imported[t] = true
			if !seen[t] {
				seen[t] = true
				queue = append(queue, t)
			}
		}
	}

	mapped := map[string]bool{}
	var suites, undeclared []string
	for _, m := range conf.maps {
		_, suite, _ := strings.Cut(m, "=")
		mapped[suite] = true
		if !isCand[suite] {
			// the engine refuses a map naming no discovered suite: every
			// report falls back to full
			undeclared = append(undeclared, suite+" (e2e_map names no discovered suite)")
		}
	}
	for _, f := range cands {
		status := "declared"
		if !mapped[f] {
			status = scopedGatesHeader(filepath.Join(clone, filepath.FromSlash(f)))
		}
		if status == "undeclared" && imported[f] {
			continue // a helper, not a suite
		}
		suites = append(suites, f)
		switch status {
		case "undeclared":
			undeclared = append(undeclared, f)
		case "malformed":
			undeclared = append(undeclared, f+" (malformed carries:)")
		}
	}
	return suites, undeclared
}

// scopedGatesHeader parses a suite's opening comment block (leading blank and
// `//` / `#` comment lines) like the engine: "declared" when it holds at least
// one carries: glob, "malformed" for an empty carries: value or a carries line
// without its colon, else "undeclared".
func scopedGatesHeader(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "undeclared"
	}
	defer f.Close()
	globs, bad := 0, false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "//") && !strings.HasPrefix(t, "#") {
			break
		}
		c := strings.TrimLeft(t, "/#")
		c = strings.TrimPrefix(c, "!")
		c = strings.TrimLeft(c, " \t")
		if rest, ok := strings.CutPrefix(c, "carries"); ok {
			r := strings.TrimLeft(rest, " \t")
			if v, ok := strings.CutPrefix(r, ":"); ok {
				if strings.TrimSpace(v) == "" {
					bad = true
				}
				globs += len(strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' }))
				continue
			}
			if rest == "" || !(rest[0] == '_' || rest[0] == '-' || rest[0] >= '0' && rest[0] <= '9' || rest[0] >= 'A' && rest[0] <= 'Z' || rest[0] >= 'a' && rest[0] <= 'z') {
				bad = true
			}
		}
	}
	switch {
	case bad:
		return "malformed"
	case globs > 0:
		return "declared"
	}
	return "undeclared"
}

// scopedGatesImports answers the repository modules one module imports, by
// the analyzer's scan (unresolvable and third-party imports are left out).
func scopedGatesImports(clone, analyzer, rel string, exists func(string) bool) []string {
	raw, err := os.ReadFile(filepath.Join(clone, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	dir := pathpkg.Dir(rel)
	var out []string
	switch analyzer {
	case "npm":
		switch pathpkg.Ext(rel) {
		case ".mjs", ".js", ".cjs", ".ts", ".tsx", ".mts", ".cts", ".jsx":
		default:
			return nil
		}
		for _, m := range scopedGatesJSImport.FindAllStringSubmatch(string(raw), -1) {
			t := pathpkg.Join(dir, m[1])
			if strings.HasPrefix(t, "../") || t == ".." {
				continue
			}
			for _, c := range []string{t, t + ".mjs", t + ".js", t + ".cjs", t + ".ts", t + ".tsx", t + ".mts", t + ".jsx",
				t + "/index.mjs", t + "/index.js", t + "/index.ts", t + "/index.tsx"} {
				if exists(c) {
					out = append(out, c)
					break
				}
			}
		}
	case "rust":
		if pathpkg.Ext(rel) != ".rs" {
			return nil
		}
		child := dir
		if b := pathpkg.Base(rel); b != "lib.rs" && b != "main.rs" && b != "mod.rs" && !(dir == "tests" && strings.HasSuffix(b, ".rs")) {
			child = strings.TrimSuffix(rel, ".rs")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if m := scopedGatesRustMod.FindStringSubmatch(line); m != nil {
				for _, c := range []string{pathpkg.Join(child, m[1]+".rs"), pathpkg.Join(child, m[1], "mod.rs")} {
					if exists(c) {
						out = append(out, c)
						break
					}
				}
			}
		}
	case "python":
		if pathpkg.Ext(rel) != ".py" {
			return nil
		}
		res := func(p string) string {
			if exists(p + ".py") {
				return p + ".py"
			}
			if exists(p + "/__init__.py") {
				return p + "/__init__.py"
			}
			return ""
		}
		abs := func(m string) {
			p := strings.ReplaceAll(m, ".", "/")
			for _, c := range []string{p, "src/" + p, pathpkg.Join(dir, p)} {
				if r := res(c); r != "" {
					out = append(out, r)
					return
				}
			}
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if i := strings.IndexByte(line, '#'); i >= 0 {
				line = line[:i]
			}
			if m := scopedGatesPyImport.FindStringSubmatch(line); m != nil {
				for _, part := range strings.Split(m[1], ",") {
					if f := strings.Fields(part); len(f) > 0 && scopedGatesPyIdent.MatchString(f[0]) {
						abs(f[0])
					}
				}
				continue
			}
			m := scopedGatesPyFrom.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			base := m[1]
			var names []string
			for _, part := range strings.Split(strings.NewReplacer("(", " ", ")", " ", "\\", " ").Replace(m[2]), ",") {
				if f := strings.Fields(part); len(f) > 0 {
					names = append(names, f[0])
				}
			}
			if !strings.HasPrefix(base, ".") {
				abs(base)
				for _, n := range names {
					abs(base + "." + n)
				}
				continue
			}
			dots := len(base) - len(strings.TrimLeft(base, "."))
			pdir := dir
			for k := 1; k < dots; k++ {
				pdir = pathpkg.Dir(pdir)
			}
			mod := strings.ReplaceAll(base[dots:], ".", "/")
			if mod != "" {
				if r := res(pathpkg.Join(pdir, mod)); r != "" {
					out = append(out, r)
				}
				for _, n := range names {
					if r := res(pathpkg.Join(pdir, mod, n)); r != "" {
						out = append(out, r)
					}
				}
				continue
			}
			for _, n := range names {
				if r := res(pathpkg.Join(pdir, n)); r != "" {
					out = append(out, r)
				}
			}
			if r := res(pathpkg.Join(pdir, "__init__")); r != "" {
				out = append(out, r)
			}
		}
	}
	return out
}
