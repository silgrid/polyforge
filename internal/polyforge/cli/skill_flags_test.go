package cli

// skill_flags_test.go: the skill↔CLI flag cross-check. Every `--flag` the
// skill surface (skills/polyforge: SKILL.md, verbs/, hooks/, extensions/)
// teaches after `pf <verb>` must exist in the CLI: the global flag set,
// the verb's own Setup flag set, or — for sub-actions that parse their
// own flags (`ops end-session`, `chat init`, `file create`, `watch`
// subcommands) — a flag literal defined in this
// package. A renamed or deleted flag that the skill still teaches fails
// here instead of in a user's session. `pf daemon` flags live in the
// daemon package and are matched against the help text that documents
// them (daemonUsage).

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	skillPfRe     = regexp.MustCompile(`\bpf ([a-z][a-z-]*)`)
	skillFlagRe   = regexp.MustCompile(`(?:^|[\s\[(|"'=])(--[a-z][a-z-]*)`)
	srcFlagDefRe  = regexp.MustCompile(`\.(?:String|Bool|Int|Int64|Duration|Func)\("([a-z][a-z-]*)"`)
	srcFlagVarRe  = regexp.MustCompile(`\.(?:Var|StringVar|BoolVar|IntVar|DurationVar)\([^,]+, "([a-z][a-z-]*)"`)
	srcFlagLitRe  = regexp.MustCompile(`"(--[a-z][a-z-]*)"`)
	srcFlagCaseRe = regexp.MustCompile(`case "(--[a-z][a-z-]*)"`)
)

func TestSkillTaughtFlagsExistInCLI(t *testing.T) {
	root := filepath.Join("..", "..", "..", "skills", "polyforge")

	// the global flag set of Run (cli.go)
	global := map[string]bool{}
	for _, n := range []string{"json", "config", "server", "api-key", "socket", "user", "version", "help", "h"} {
		global["--"+n] = true
	}
	verbFlags := map[string]map[string]bool{}
	for _, c := range commands {
		fs := flag.NewFlagSet(c.Name, flag.ContinueOnError)
		if c.Setup != nil {
			c.Setup(fs)
		}
		set := map[string]bool{}
		fs.VisitAll(func(f *flag.Flag) { set["--"+f.Name] = true })
		verbFlags[c.Name] = set
	}
	// flags the package's sub-actions define or match themselves
	pkg := map[string]bool{}
	srcs, _ := filepath.Glob("*.go")
	for _, p := range srcs {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{srcFlagDefRe, srcFlagVarRe} {
			for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
				pkg["--"+m[1]] = true
			}
		}
		for _, re := range []*regexp.Regexp{srcFlagLitRe, srcFlagCaseRe} {
			for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
				pkg[m[1]] = true
			}
		}
	}

	checked := 0
	var bad []string
	err := filepath.WalkDir(root, func(path string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".md", ".sh", ".ts":
		default:
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for ln, line := range strings.Split(string(raw), "\n") {
			locs := skillPfRe.FindAllStringSubmatchIndex(line, -1)
			for i, loc := range locs {
				verb := line[loc[2]:loc[3]]
				vf, ok := verbFlags[verb]
				if !ok {
					continue // not a pf verb (pf-chat, prose)
				}
				end := len(line)
				if i+1 < len(locs) {
					end = locs[i+1][0]
				}
				seg := line[loc[1]:end]
				// stop at a shell boundary: a flag after |, ;, && or a
				// comment belongs to another command
				if j := strings.IndexAny(seg, "|;#"); j >= 0 && !strings.Contains(seg[:j], "[") {
					seg = seg[:j]
				}
				for _, m := range skillFlagRe.FindAllStringSubmatch(seg, -1) {
					f := m[1]
					checked++
					known := global[f] || vf[f] || pkg[f]
					if verb == "daemon" {
						known = known || strings.Contains(daemonUsage, f+" ") || strings.Contains(daemonUsage, f+"\n") || strings.Contains(daemonUsage, f+"]")
					}
					if !known {
						rel, _ := filepath.Rel(root, path)
						bad = append(bad, rel+":"+strconv.Itoa(ln+1)+": pf "+verb+" "+f)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"--statusline", "--harness-session", "--wait-decision", "--reason"} {
		if !pkg[must] && !verbFlags["watch"][must] && !verbFlags["ops"][must] {
			t.Errorf("the CLI defines no %s flag, but the skill relies on it", must)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d taught flags found — the scan lost its subject", checked)
	}
	if len(bad) > 0 {
		t.Fatalf("flags taught by skills/polyforge that the CLI does not define:\n%s", strings.Join(bad, "\n"))
	}
}
