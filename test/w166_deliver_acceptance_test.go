package test

// w166_deliver_acceptance_test.go: the deliver world the remaining e2e
// scenarios share (the develop lane's publish and merge, the bar lock):
// task repos whose origins are local bare repos and a fake `gh` first on
// the daemon's PATH that keeps PRs as files and performs `gh pr merge
// --merge` as a real --no-ff merge. Its own deliver-step scenarios drove
// only the retired lanes and are deleted (owner 2026-11-09: deprecated
// lanes retired, their tests are not maintained).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGHPRs is the stand-in for GitHub's PR API. It ignores --jq and
// prints the one value each scripted call asks for; a `pr view` asking for
// the PR's head and base ref (the merge step's merge.py, gh pr view)
// answers the JSON object gh would.
const fakeGHPRs = `#!/bin/sh
echo "gh $*" >> "$FAKE_GH_STATE/calls.log"
origin=$(git remote get-url origin 2>/dev/null) || { echo "fake gh: not in a repo with an origin" >&2; exit 2; }
S="$FAKE_GH_STATE/$(basename "$origin" .git)"; mkdir -p "$S/prs"
[ "$1" = pr ] || { echo "fake gh: unsupported: $*" >&2; exit 2; }
sub=$2; shift 2
head=""; state=all; base=main; target=""; method=""; json=""; match=""
while [ $# -gt 0 ]; do
  case "$1" in
    --head) head=$2; shift 2 ;;
    --state) state=$2; shift 2 ;;
    --base) base=$2; shift 2 ;;
    --json) json=$2; shift 2 ;;
    --match-head-commit) match=$2; shift 2 ;;
    -q|--jq|--title|--body|--body-file) shift 2 ;;
    --merge|--squash|--rebase) method=$1; shift ;;
    -*) shift ;;
    *) target=$1; shift ;;
  esac
done
case "$sub" in
  list)
    for d in "$S"/prs/*; do
      [ -d "$d" ] && [ "$(cat "$d/head")" = "$head" ] || continue
      st=$(cat "$d/state")
      case "$state" in
        open) [ "$st" = OPEN ] || continue ;;
        merged) [ "$st" = MERGED ] || continue ;;
      esac
      cat "$d/url"
    done ;;
  create)
    for d in "$S"/prs/*; do
      if [ -d "$d" ] && [ "$(cat "$d/head")" = "$head" ] && [ "$(cat "$d/state")" = OPEN ]; then
        echo "a pull request for branch \"$head\" already exists" >&2; exit 1
      fi
    done
    n=$(( $(ls "$S/prs" | wc -l) + 1 )); d="$S/prs/$n"; mkdir -p "$d"
    echo "$head" > "$d/head"; echo "$base" > "$d/base"; echo OPEN > "$d/state"
    echo "https://github.com/fake/$(basename "$S")/pull/$n" > "$d/url"; cat "$d/url" ;;
  merge)
    d="$S/prs/${target##*/}"
    [ "$method" = --merge ] || { echo "fake gh: refusing merge method '$method'" >&2; exit 1; }
    [ "$(cat "$d/state")" = OPEN ] || { echo "fake gh: PR not open" >&2; exit 1; }
    if [ -n "$match" ] && [ "$(git ls-remote "$origin" "refs/heads/$(cat "$d/head")" | cut -f1)" != "$match" ]; then
      echo "fake gh: the PR's head is not $match" >&2; exit 1
    fi
    w=$(mktemp -d); trap 'rm -rf "$w"' EXIT # a refused merge (a conflict) leaves nothing behind
    git clone -q "$origin" "$w/c" || exit 1
    (cd "$w/c" && git checkout -q "$(cat "$d/base")" &&
      git merge -q --no-ff -m "Merge pull request #${target##*/} from $(cat "$d/head")" "origin/$(cat "$d/head")" &&
      git push -q origin "HEAD:refs/heads/$(cat "$d/base")" && git rev-parse HEAD > "$d/merge") || exit 1
    echo MERGED > "$d/state"; rm -rf "$w" ;;
  view)
    d="$S/prs/${target##*/}"
    case "$json" in
      *headRefName*)
        # the daemon's merge step reads the PR whole
        mc=null; [ -s "$d/merge" ] && mc="{\"oid\":\"$(cat "$d/merge")\"}"
        printf '{"state":"%s","headRefName":"%s","baseRefName":"%s","mergeCommit":%s}\n' "$(cat "$d/state")" "$(cat "$d/head")" "$(cat "$d/base")" "$mc" ;;
      *) cat "$d/merge" ;;
    esac ;;
esac
`

// deliverWorld is one scenario's fake GitHub: bare origins (main seeded
// with a baseline), the fake gh and its state dir.
type deliverWorld struct {
	t     *testing.T
	root  string
	state string
	bin   string
}

func newDeliverWorld(t *testing.T) *deliverWorld {
	t.Helper()
	root := shortTmp(t, "pfw166")
	t.Cleanup(func() { os.RemoveAll(root) })
	w := &deliverWorld{t: t, root: root, state: filepath.Join(root, "gh"), bin: filepath.Join(root, "bin")}
	for _, d := range []string{w.bin, w.state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(w.bin, "gh"), []byte(fakeGHPRs), 0o755); err != nil {
		t.Fatal(err)
	}
	// a carried e2e bar command (`npm run test:e2e:all`) has
	// no package.json in this world: the shim stands in for a passing run.
	if err := os.WriteFile(filepath.Join(w.bin, "npm"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return w
}

// origin creates the bare origin <name>.git with a main baseline.
func (w *deliverWorld) origin(name string) string {
	t := w.t
	t.Helper()
	bare := filepath.Join(w.root, name+".git")
	gitRun(t, "", "init", "-q", "--bare", "-b", "main", bare)
	seed := filepath.Join(w.root, "seed-"+name)
	gitRun(t, "", "clone", "-q", bare, seed)
	if err := os.WriteFile(filepath.Join(seed, "README"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "add", "-A")
	gitRun(t, seed, "commit", "-q", "-m", "baseline")
	gitRun(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	return bare
}

// daemonEnv is the delivery world's daemon environment. The fake gh comes
// first on PATH, then the pf under test (v1Bins): a step's own
// `pf` call (the deliver step's `pf bar-run`) resolves to
// the build the daemon runs, exactly as an installed daemon's steps resolve
// to the installed pf — never to whatever release happens to be on the
// machine's PATH.
func (w *deliverWorld) daemonEnv() daemonOpt {
	sep := string(os.PathListSeparator)
	return optEnv("PATH="+w.bin+sep+v1BinDir+sep+os.Getenv("PATH"), "FAKE_GH_STATE="+w.state)
}

// prState answers repo's PR n state ("" when absent).
func (w *deliverWorld) prState(repo, n string) string {
	b, _ := os.ReadFile(filepath.Join(w.state, repo, "prs", n, "state"))
	return strings.TrimSpace(string(b))
}

// calls counts the fake gh invocations starting with prefix.
func (w *deliverWorld) calls(prefix string) int {
	b, _ := os.ReadFile(filepath.Join(w.state, "calls.log"))
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

// --- the scripted deliver procedure (steps/deliver.md), per repo. "$b" is
// the feature branch; literalBranch spells it out before the plan is
// written (the driver reads pushes literally).

func dPre(repo string) string {
	return `set -e; cap="$PWD"; mkdir -p artifacts; cd repos/` + repo + `; `
}

// literalBranch spells the feature branch out in scripted commands.
func literalBranch(branch string, cmds ...string) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = strings.ReplaceAll(c, "$b", branch)
	}
	return out
}

// newDeliverWI files a WI on bundle with an empty plan and answers it and
// its feature branch (the plan is written once the branch is known).
func newDeliverWI(t *testing.T, inst *v1Install, goal, bundle string) (string, string) {
	t.Helper()
	inst.keepDeliver = true
	wi := newWIWithBundle(t, inst, goal, bundle, nil)
	return wi, "polyforge/" + wiPathName(t, inst, wi)
}

func startDeliverDaemon(t *testing.T, inst *v1Install, w *deliverWorld, wi string) *v1Daemon {
	t.Helper()
	d := newV1Daemon(t, inst, w.daemonEnv())
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	return d
}
