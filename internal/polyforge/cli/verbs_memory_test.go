package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Canned memory faces for `pf memory show`: the default JSON metadata
// (memory.Entry — no content, per the owner-verified v1 server) and the
// ?format=content body as text/markdown. Non-canonical key order and an
// unknown field in the metadata — --json must hand those bytes through
// verbatim plus one CLI-added "content" member.
const memoryMetaCanned = `{"updated_at":"2026-10-02T00:00:00Z","id":"mem_1","type":"experience.debug",` +
	`"title":"the title","visibility":"project","content_len":21,"future_field":true}`
const memoryBodyCanned = "# heading\n\nbody text\n"

// newFakeMemoryHub serves the two memory faces for id mem_1 (and MEM-404
// for any other id).
func memHub(t *testing.T, rec *reqRecorder) string {
	t.Helper()
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") == "content" {
			if r.URL.Path == "/api/v1/memories/mem_1" {
				w.Header().Set("Content-Type", "text/markdown")
				_, _ = w.Write([]byte(memoryBodyCanned))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"MEM-404","detail":"no such memory"}`))
			return
		}
		if r.URL.Path == "/api/v1/memories/mem_1" {
			_, _ = w.Write([]byte(memoryMetaCanned))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"MEM-404","detail":"no such memory"}`))
	})
	return srv.URL
}

// TestVerbMemoryShowRequestShape pins the two-request wire shape: the
// metadata GET first, the ?format=content body GET second, both with the
// bearer key. There is no --project flag — the id is the only argument.
func TestVerbMemoryShowRequestShape(t *testing.T) {
	var rec reqRecorder
	srv := memHub(t, &rec)
	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER": srv, "PF_API_KEY": "k_cli",
	}, "memory", "show", "mem_1")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("want 2 requests (metadata + content), got %d", len(reqs))
	}
	if reqs[0].Method != "GET" || reqs[0].Path != "/api/v1/memories/mem_1" {
		t.Errorf("metadata request = %s %s", reqs[0].Method, reqs[0].Path)
	}
	if reqs[1].Method != "GET" || reqs[1].Path != "/api/v1/memories/mem_1?format=content" {
		t.Errorf("content request = %s %s", reqs[1].Method, reqs[1].Path)
	}
	for i, want := range []string{"Bearer k_cli", "Bearer k_cli"} {
		if reqs[i].Header.Get("Authorization") != want {
			t.Errorf("request %d auth = %q", i, reqs[i].Header.Get("Authorization"))
		}
	}
	// Human mode: metadata header (id, type, title, visibility, updated,
	// content_len), blank line, then the body; no truncation line under
	// the default cap.
	for _, want := range []string{
		"id: mem_1", "type: experience.debug", "title: the title",
		"visibility: project", "updated: 2026-10-02T00:00:00Z",
		"content_len: 21", "\n# heading\n\nbody text\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("human output missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "truncated") {
		t.Errorf("human output carries a truncation line under the default cap:\n%s", stdout)
	}
}

// TestVerbMemoryShowTruncation pins the human-mode byte cap: --max-bytes N
// caps the body at exactly N bytes and the output ends with the exact
// notice (the shown count is the effective cap, <total> the full body
// length); --max-bytes 0 disables the cap entirely.
func TestVerbMemoryShowTruncation(t *testing.T) {
	srv := memHub(t, &reqRecorder{})
	env := map[string]string{"PF_SERVER": srv, "PF_API_KEY": "k_cli"}
	code, stdout, stderr := runPf(t, env, "memory", "show", "mem_1", "--max-bytes", "4")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	want := "[truncated: showed 4 of 21 bytes — use --max-bytes 0 for the full body]\n"
	if !strings.HasSuffix(stdout, want) {
		t.Fatalf("truncation notice:\n got: %q\nwant suffix: %q", stdout, want)
	}
	if !strings.Contains(stdout, "# he\n[truncated") {
		t.Errorf("capped body (4 bytes, notice on its own line) missing:\n%s", stdout)
	}
	if strings.Contains(stdout, memoryBodyCanned) {
		t.Errorf("capped run showed the whole body:\n%s", stdout)
	}
	// --max-bytes 0: unlimited, no truncation line.
	code, stdout, stderr = runPf(t, env, "memory", "show", "mem_1", "--max-bytes", "0")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, memoryBodyCanned) {
		t.Errorf("--max-bytes 0 dropped body text:\n%s", stdout)
	}
	if strings.Contains(stdout, "truncated") {
		t.Errorf("--max-bytes 0 carries a truncation line:\n%s", stdout)
	}
	// Negative cap: a loud local refusal, no wire contact.
	code, _, stderr = runPf(t, env, "memory", "show", "mem_1", "--max-bytes", "-1")
	if code == 0 {
		t.Fatalf("negative --max-bytes accepted")
	}
	if !strings.Contains(stderr, "memory show: --max-bytes must be >= 0") {
		t.Errorf("negative --max-bytes stderr = %q", stderr)
	}
}

// TestVerbMemoryShowJSON pins the #132 --json shape: the server's
// metadata JSON VERBATIM (byte-for-byte, non-canonical key order kept)
// plus one CLI-added top-level "content" string with the FULL body —
// never truncated regardless of --max-bytes.
func TestVerbMemoryShowJSON(t *testing.T) {
	srv := memHub(t, &reqRecorder{})
	env := map[string]string{"PF_SERVER": srv, "PF_API_KEY": "k_cli"}
	code, stdout, stderr := runPf(t, env, "--json", "memory", "show", "mem_1", "--max-bytes", "4")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	want := memoryMetaCanned[:len(memoryMetaCanned)-1] + `,"content":` +
		jsonEscape(t, memoryBodyCanned) + "}\n"
	if stdout != want {
		t.Fatalf("--json output not metadata-verbatim + content:\n got: %s\nwant: %s", stdout, want)
	}
	// The added field is a top-level string member of one JSON object.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("--json output is not one JSON object: %v", err)
	}
	var content string
	if err := json.Unmarshal(doc["content"], &content); err != nil {
		t.Fatalf("content field: %v", err)
	}
	if content != memoryBodyCanned {
		t.Fatalf("--json content truncated despite --max-bytes 4: %q", content)
	}
}

// TestVerbMemoryShow404 pins the invisible/unknown-id semantics: the
// server's 404 surfaces as the typed CLI error with exit 1, and the body
// request is never sent (metadata fails first — no probing leak).
func TestVerbMemoryShow404(t *testing.T) {
	var rec reqRecorder
	srv := memHub(t, &rec)
	env := map[string]string{"PF_SERVER": srv, "PF_API_KEY": "k_cli"}
	code, _, stderr := runPf(t, env, "memory", "show", "ghost")
	if code != 1 {
		t.Fatalf("404 exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "MEM-404") {
		t.Errorf("404 stderr missing the typed code: %q", stderr)
	}
	if n := len(rec.all()); n != 1 {
		t.Fatalf("404 made %d requests, want exactly 1 (metadata only — no probing)", n)
	}
}

// TestVerbMemoryShowLocalValidation pins the local refusals: no id, an
// unknown action, and --project (there is deliberately no such flag —
// ids are globally unique) all fail before any wire contact. All three
// are client-class failures: exit 1 (the same family as the other
// verbs' local validation; only top-level parse errors exit 2).
func TestVerbMemoryShowLocalValidation(t *testing.T) {
	var rec reqRecorder
	srv := memHub(t, &rec)
	env := map[string]string{"PF_SERVER": srv, "PF_API_KEY": "k_cli"}
	if code, _, stderr := runPf(t, env, "memory", "show"); code != 1 ||
		!strings.Contains(stderr, "expected exactly one memory id") {
		t.Errorf("missing id: code=%d stderr=%q, want 1 + guidance", code, stderr)
	}
	if code, _, stderr := runPf(t, env, "memory", "bogus", "mem_1"); code != 1 ||
		!strings.Contains(stderr, "unknown action") {
		t.Errorf("unknown action: code=%d stderr=%q, want 1", code, stderr)
	}
	if code, _, stderr := runPf(t, env, "memory", "show", "mem_1", "--project", "p"); code != 1 ||
		!strings.Contains(stderr, "project") {
		t.Errorf("--project accepted: code=%d stderr=%q, want 1 (no such flag)", code, stderr)
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("local refusals made %d requests, want 0", n)
	}
}

// jsonEscape marshals s as one JSON string (the same spelling the
// merge writes into the --json document).
func jsonEscape(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}
