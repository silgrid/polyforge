package taskrepo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestNamesAreTheRepoDirectories: every directory under repos/ not
// starting with a dot, sorted; a file or a missing directory is none.
func TestNamesAreTheRepoDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"b", "a", ".hidden"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Names(dir); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("Names = %v", got)
	}
	if got := Names(filepath.Join(dir, "none")); got != nil {
		t.Fatalf("a missing directory: %v", got)
	}
}

// TestGitIsHardened: an inherited GIT_DIR, the programs a repository's
// own config can name (a hook, an fsmonitor, an external diff, an ssh
// command) and a failing read's stderr — the read runs in dir's own
// repository, runs none of those programs, and names git's refusal.
func TestGitIsHardened(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	tracked := filepath.Join(dir, "file")
	run("init", "-q")
	if err := os.WriteFile(tracked, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "file")
	run("commit", "-q", "-m", "one")
	if err := os.WriteFile(tracked, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-q", "-am", "two")
	marks := t.TempDir()
	planted := func(name string) (script, marker string) {
		t.Helper()
		marker = filepath.Join(marks, name+"-ran")
		script = filepath.Join(marks, name)
		if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return script, marker
	}
	hook, hookRan := planted("hook")
	if err := os.Rename(hook, filepath.Join(dir, ".git", "hooks", "reference-transaction")); err != nil {
		t.Fatal(err)
	}
	fsmonitor, fsmonitorRan := planted("fsmonitor")
	run("config", "core.fsmonitor", fsmonitor)
	extDiff, extDiffRan := planted("ext-diff")
	run("config", "diff.external", extDiff)
	sshCmd, sshRan := planted("ssh")
	run("config", "core.sshCommand", sshCmd)
	// The ssh git's own default names: on PATH, failing at once, no network.
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "ssh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "elsewhere"))

	out, err := Git(dir, "rev-parse", "--verify", "HEAD")
	if err != nil || len(strings.TrimSpace(out)) != 40 {
		t.Fatalf("the read in dir's own repository: %q %v", out, err)
	}
	if _, err := Git(dir, "update-ref", "refs/heads/x", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hookRan); err == nil {
		t.Error("a repo-local hook ran")
	}
	_, _ = Git(dir, "status", "--porcelain")
	if _, err := os.Stat(fsmonitorRan); err == nil {
		t.Error("a repo-local fsmonitor ran")
	}
	_, _ = Git(dir, "diff", "HEAD~1", "HEAD")
	if _, err := os.Stat(extDiffRan); err == nil {
		t.Error("a repo-local external diff ran")
	}
	_, _ = Git(dir, "ls-remote", "ssh://example.invalid/x")
	if _, err := os.Stat(sshRan); err == nil {
		t.Error("a repo-local ssh command ran")
	}
	if _, err := Git(dir, "rev-parse", "--verify", "no-such-ref"); err == nil || !strings.Contains(err.Error(), "git rev-parse --verify no-such-ref: ") {
		t.Fatalf("a failing read names git and its refusal: %v", err)
	}
}

// gitStartsAllowed are the production git starts outside Command, keyed
// "<file relative to internal/polyforge> <function>", each with its reason.
var gitStartsAllowed = map[string]string{
	"taskrepo/taskrepo.go Command": "the hardened entry itself",
	"runtime/repos.go gitClone": "the clone creates the task repo: before it there is no repo-level config to plant, " +
		"and routing it through Command would change the clone's credentials, proxy and ssh context for no safety gain",
}

// TestEveryGitCallGoesThroughCommand: every production process start under
// internal/polyforge whose program is git (a "git" literal, a path ending
// in /git, or a same-package constant holding one, anywhere in the program
// argument) is in Command or on the allowlist, and every allowlist row is
// met exactly once — a new bypass is red, and so is a stale row or a scan
// that went blind.
func TestEveryGitCallGoesThroughCommand(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	type parsed struct {
		rel  string
		file *ast.File
	}
	fset := token.NewFileSet()
	byDir := map[string][]parsed{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		byDir[filepath.Dir(path)] = append(byDir[filepath.Dir(path)], parsed{filepath.ToSlash(rel), f})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(byDir) == 0 {
		t.Fatalf("no Go source under %s", root)
	}
	isGit := func(v string) bool { return v == "git" || strings.HasSuffix(v, "/git") }
	// program answers the index of the program argument of a process start.
	program := map[string]map[string]int{
		"os/exec": {"Command": 0, "CommandContext": 1},
		"os":      {"StartProcess": 0},
		"syscall": {"Exec": 0, "ForkExec": 0},
	}
	hits := map[string]int{}
	var bypasses []string
	for _, files := range byDir {
		gitConsts := map[string]bool{}
		for _, p := range files {
			for _, decl := range p.file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if v, err := strconv.Unquote(lit.Value); err == nil && isGit(v) {
								gitConsts[name.Name] = true
							}
						}
					}
				}
			}
		}
		for _, p := range files {
			imports := map[string]map[string]int{}
			for _, imp := range p.file.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				fns, ok := program[path]
				if !ok {
					continue
				}
				name := filepath.Base(path)
				if imp.Name != nil {
					name = imp.Name.Name
				}
				if name == "_" || name == "." {
					continue
				}
				imports[name] = fns
			}
			if len(imports) == 0 {
				continue
			}
			for _, decl := range p.file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkg, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					idx, ok := imports[pkg.Name][sel.Sel.Name]
					if !ok || idx >= len(call.Args) {
						return true
					}
					startsGit := false
					ast.Inspect(call.Args[idx], func(m ast.Node) bool {
						switch x := m.(type) {
						case *ast.BasicLit:
							if v, err := strconv.Unquote(x.Value); x.Kind == token.STRING && err == nil && isGit(v) {
								startsGit = true
							}
						case *ast.Ident:
							if gitConsts[x.Name] {
								startsGit = true
							}
						}
						return !startsGit
					})
					if !startsGit {
						return true
					}
					key := p.rel + " " + fn.Name.Name
					hits[key]++
					if _, ok := gitStartsAllowed[key]; !ok {
						bypasses = append(bypasses, p.rel+":"+strconv.Itoa(fset.Position(call.Pos()).Line)+" ("+fn.Name.Name+")")
					}
					return true
				})
			}
		}
	}
	sort.Strings(bypasses)
	if len(bypasses) > 0 {
		t.Errorf("git started outside taskrepo.Command:\n  %s", strings.Join(bypasses, "\n  "))
	}
	for key := range gitStartsAllowed {
		if hits[key] != 1 {
			t.Errorf("allowlist row %q met %d times, want exactly 1", key, hits[key])
		}
	}
}
