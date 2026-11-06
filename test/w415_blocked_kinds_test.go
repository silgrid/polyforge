package test

// w415_blocked_kinds_test.go (PR #860 code review): Pi Web's item
// panel labels a blocked work item by its kind and offers 继续 for every kind
// (every daemon block is a non-terminal state `pf resume` releases). This test
// holds pi-web's KNOWN_BLOCKED_KINDS (pi-web/lib/polyforge-decisions-pure.ts)
// equal to the kinds the daemon's watch snapshot can carry — the literals
// `watchBlocked` (internal/polyforge/daemon/watchsnap.go) assigns, and the
// driver's `BlockerKind*` constants it names — so a kind added on the Go side
// fails here until the panel knows it.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// sameSet fails when two name sets differ, naming both sides (it moved here
// from the deleted w413 discussion-liveness pin, workflow contract v2
// change 14).
func sameSet(t *testing.T, what string, got, want map[string]bool) {
	t.Helper()
	var g, w []string
	for k := range got {
		g = append(g, k)
	}
	for k := range want {
		w = append(w, k)
	}
	sort.Strings(g)
	sort.Strings(w)
	if strings.Join(g, ",") != strings.Join(w, ",") {
		t.Fatalf("%s: pi-web has %v, the daemon has %v", what, g, w)
	}
}

func TestW415BlockedKindsMatchTheDaemon(t *testing.T) {
	root := e2e344Root(t)
	snap := e2e344Read(t, root, "internal/polyforge/daemon/watchsnap.go")
	body := regexp.MustCompile(`(?s)func watchBlocked\(.*?\n\}`).FindString(snap)
	if body == "" {
		t.Fatal("watchBlocked not found in internal/polyforge/daemon/watchsnap.go")
	}

	// The driver's BlockerKind* constants, by name.
	constants := map[string]string{}
	driverDir := filepath.Join(root, "internal/polyforge/driver")
	entries, err := os.ReadDir(driverDir)
	if err != nil {
		t.Fatalf("read the driver: %v", err)
	}
	constPattern := regexp.MustCompile(`\b(BlockerKind\w+)\s*(?:string\s*)?=\s*"([^"]+)"`)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, m := range constPattern.FindAllStringSubmatch(e2e344Read(t, root, filepath.Join("internal/polyforge/driver", name)), -1) {
			constants[m[1]] = m[2]
		}
	}
	if len(constants) == 0 {
		t.Fatal("no driver BlockerKind* constant found — the pattern no longer matches")
	}

	// Every kind watchBlocked can assign: `kind := "x"`, `kind = "x"`,
	// `Kind: "x"` literals and every driver.BlockerKind* it names.
	daemon := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?:\bkind\s*:?=\s*|\bKind:\s*)"([^"]+)"`).FindAllStringSubmatch(body, -1) {
		daemon[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`driver\.(BlockerKind\w+)`).FindAllStringSubmatch(body, -1) {
		value, ok := constants[m[1]]
		if !ok {
			t.Fatalf("watchBlocked names driver.%s, which the driver does not define as a string constant", m[1])
		}
		daemon[value] = true
	}
	if len(daemon) < 3 {
		t.Fatalf("watchBlocked parse found only %v — the pattern no longer matches", daemon)
	}

	source := e2e344Read(t, root, "pi-web/lib/polyforge-decisions-pure.ts")
	block := regexp.MustCompile(`(?s)KNOWN_BLOCKED_KINDS: readonly string\[\] = \[(.*?)\];`).FindStringSubmatch(source)
	if block == nil {
		t.Fatal("KNOWN_BLOCKED_KINDS not found in pi-web/lib/polyforge-decisions-pure.ts")
	}
	piWeb := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(block[1], -1) {
		piWeb[m[1]] = true
	}
	sameSet(t, "KNOWN_BLOCKED_KINDS vs the kinds watchBlocked assigns", piWeb, daemon)
}
