// carries: internal/aihub/server/*, internal/aihub/core/*, internal/common/api/types.go, docs/upgrade.md

package server

// the send-back door and its owner-rewind/applied settle
// lost their last client with workflow contract 4 C3c, so aihub deletes
// them: both paths are unknown to the mux (its own 404, not a handler's
// concealing CORE-404), no code anywhere in the repository implements or
// calls them, and the deployment note states there is no SQL migration.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestW495SendBackRoutesAnswer404(t *testing.T) {
	e := newSrvEnv(t)
	id := sealedBlockedWI(t, e, "send-back gone")
	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"/api/v1/work-items/" + id + "/send-back", map[string]any{"id": "rw_1", "to": "grill", "reason": "r"}},
		{"/api/v1/work-items/" + id + "/owner-rewind/applied", map[string]any{"session_id": strings.Repeat("a1", 16), "id": "rw_1"}},
	} {
		code, raw := e.do(t, "POST", c.path, e.key, c.body, nil)
		if code != 404 || strings.TrimSpace(string(raw)) != "404 page not found" {
			t.Errorf("POST %s: %d %q, want the mux's own 404 page not found", c.path, code, raw)
		}
	}
	attrs, _ := wiEvents(t, e, id)
	if _, has := attrs["owner_rewind"]; has {
		t.Errorf("attrs carry owner_rewind after the requests: %v", attrs)
	}
	var wi struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || wi.Status != api.StatusBlocked {
		t.Errorf("after the requests: %d %s, want the work item still blocked", code, raw)
	}
}

// w495Patterns name the deleted implementation: the routes, their
// handlers and core method, the request types, the old client methods,
// the attrs marker helpers and the events only they wrote.
var w495Patterns = []*regexp.Regexp{
	regexp.MustCompile(`/send-back\b`),
	regexp.MustCompile(`owner-rewind/applied`),
	regexp.MustCompile(`hSendBackWI`),
	regexp.MustCompile(`hOwnerRewindApplied`),
	regexp.MustCompile(`OwnerRewindApplied`),
	regexp.MustCompile(`SendBackRequest`),
	regexp.MustCompile(`SendBack[A-Z(]`),
	regexp.MustCompile(`sendBack[A-Z]`),
	regexp.MustCompile(`OwnerRewindMarker`),
	regexp.MustCompile(`OwnerRewindOf`),
	regexp.MustCompile(`OwnerRewindAttr`),
	regexp.MustCompile(`MaxOwnerRewindReasonBytes`),
	regexp.MustCompile(`owner_rewind_(recorded|applied|dropped)`),
}

var w495CodeExt = map[string]bool{".go": true, ".ts": true, ".tsx": true, ".js": true, ".mjs": true, ".py": true, ".sh": true}

var w495SkipDir = map[string]bool{".git": true, "node_modules": true, ".next": true, "dist": true, "test-results": true, "playwright-report": true}

func w495RepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the package directory")
		}
		dir = parent
	}
}

func TestW495NoSendBackImplementationInTheRepo(t *testing.T) {
	root := w495RepoRoot(t)
	self := filepath.Join(root, "internal", "aihub", "server", "w495_send_back_gone_test.go")
	scanned := 0
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && w495SkipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !w495CodeExt[filepath.Ext(path)] || path == self {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		scanned++
		seen[rel] = true
		for i, line := range strings.Split(string(b), "\n") {
			for _, re := range w495Patterns {
				if re.MatchString(line) {
					t.Errorf("%s:%d matches %s: %s", rel, i+1, re, strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if scanned < 500 {
		t.Fatalf("scanned %d code files, want at least 500 (a vacuous scan)", scanned)
	}
	for _, must := range []string{"internal/aihub/server/server.go", "internal/polyforge/aihubclient/client.go"} {
		if !seen[must] {
			t.Fatalf("the scan did not read %s", must)
		}
	}
}

func TestW495UpgradeNoteStatesNoSQLMigration(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(w495RepoRoot(t), "docs", "upgrade.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The section is found by its heading's own words: a ticket reference in
	// the heading is not part of every copy of this file.
	const heading = "## aihub: the send-back and owner-rewind/applied routes are gone"
	var section string
	lines := strings.Split(string(b), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, heading) {
			end := len(lines)
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "## ") {
					end = j
					break
				}
			}
			section = strings.Join(lines[i:end], "\n")
			break
		}
	}
	if section == "" {
		t.Fatalf("docs/upgrade.md has no section headed %q", heading)
	}
	for _, want := range []string{"/send-back", "owner-rewind/applied", "No SQL migration"} {
		if !strings.Contains(section, want) {
			t.Errorf("the send-back removal's upgrade section does not contain %q", want)
		}
	}
}
