package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

func TestCheckFileExpectationRefusesStaleHashWithDiff(t *testing.T) {
	raw := []byte("server = \"http://127.0.0.1:1\"\napi_key = \"OLD\"\n\n[daemon]\nmax_concurrent_jobs = 6\n")
	exp := configExpectation{hash: strings.Repeat("a", 64), hashSet: true}
	err := checkFileExpectation(exp, raw, true)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrCoreConflict {
		t.Fatalf("stale hash must be a CORE-409 refusal, got %v", err)
	}
	if !strings.Contains(ae.Detail, "max_concurrent_jobs") || !strings.Contains(ae.Detail, "6") {
		t.Fatalf("the refusal must name the changed key and its current value: %q", ae.Detail)
	}
	if strings.Contains(string(ae.Extra), "OLD") {
		t.Fatalf("api_key values must never appear in the diff: %s", ae.Extra)
	}
}

func TestCheckFileExpectationAcceptsMatchingHashAndAbsent(t *testing.T) {
	raw := []byte("server = \"x\"\n")
	if err := checkFileExpectation(configExpectation{hash: hashOf(raw), hashSet: true}, raw, true); err != nil {
		t.Fatalf("a matching hash must pass: %v", err)
	}
	if err := checkFileExpectation(configExpectation{hash: "absent", hashSet: true}, nil, false); err != nil {
		t.Fatalf("an absent expectation on an absent file must pass: %v", err)
	}
	if err := checkFileExpectation(configExpectation{hash: "absent", hashSet: true}, raw, true); err == nil {
		t.Fatal("an absent expectation on an existing file must refuse")
	}
}

func TestParseConfigWriteArgsKeepsNegativeValuePositional(t *testing.T) {
	pos, exp, ws, cwd, err := parseConfigWriteArgs([]string{"stall_window", "-1m", "--expect-hash", "abc", "--workspace", "/ws"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 2 || pos[0] != "stall_window" || pos[1] != "-1m" {
		t.Fatalf("negative values stay positional: %v", pos)
	}
	if !exp.hashSet || exp.hash != "abc" || ws != "/ws" || cwd != "" {
		t.Fatalf("options parsed wrong: %+v ws=%q cwd=%q", exp, ws, cwd)
	}
}

// TestCheckExpectationAcceptsUnchangedSnapshot pins the documented
// --expect-state contract: a snapshot that still matches the file — api_key
// included as {set:true} — is not a difference and must not refuse.
func TestCheckExpectationAcceptsUnchangedSnapshot(t *testing.T) {
	raw := []byte("server = \"http://127.0.0.1:1\"\napi_key = \"OLD\"\n\n[daemon]\nmax_concurrent_jobs = 6\n")
	state := `{"file_sha256":"` + hashOf(raw) + `","values":{"max_concurrent_jobs":"6","api_key":{"set":true}}}`
	exp := configExpectation{state: mustExpectState(t, state)}
	if err := checkFileExpectation(exp, raw, true); err != nil {
		t.Fatalf("an unchanged snapshot with a matching hash must pass: %v", err)
	}
}

// TestCheckExpectationWorkspaceKeyDiff pins the shared CAS routine on the
// workspace yaml: a moved summarize value is named at key level, and an
// unchanged api_key never refuses a matching file.
func TestCheckExpectationWorkspaceKeyDiff(t *testing.T) {
	exp := configExpectation{state: mustExpectState(t, `{"file_sha256":null,"values":{"summarize.interval_sec":30,"summarize.models":["pi:zeta"]}}`)}
	cur := map[string]any{
		"summarize.interval_sec": 90,
		"summarize.models":       []string{"pi:zeta"},
		"summarize.language":     "English",
	}
	err := checkExpectation(exp, "", false, cur)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrCoreConflict {
		t.Fatalf("a moved summarize value must be a CORE-409 refusal, got %v", err)
	}
	if !strings.Contains(ae.Detail, "summarize.interval_sec") {
		t.Fatalf("the workspace diff must name the changed summarize key: %q", ae.Detail)
	}
}

func TestExpectsAbsent(t *testing.T) {
	if !expectsAbsent(configExpectation{hash: "absent", hashSet: true}) {
		t.Fatal("--expect-hash absent must mean absent")
	}
	// The field must be decoded from the WIRE, not built as a Go value: an
	// explicit JSON null has to mean absent even though encoding/json would
	// decode it to the same nil a *string gives for an omitted field.
	_, exp, _, _, err := parseConfigWriteArgs([]string{"summarize.models", "--workspace", "/ws", "--expect-state", `{"file_sha256":null,"values":{}}`})
	if err != nil {
		t.Fatal(err)
	}
	if !expectsAbsent(exp) {
		t.Fatal("a wire null file_sha256 must mean absent")
	}
	if expectsAbsent(configExpectation{hash: strings.Repeat("a", 64), hashSet: true}) {
		t.Fatal("a concrete hash is not absent")
	}
	if expectsAbsent(configExpectation{}) {
		t.Fatal("no expectation is not absent")
	}
}

// TestWireNullFileSHA256DrivesAbsentAndStale pins the tri-state decoding that
// a *string could not express: a JSON null is "expected absent", not "no hash
// expectation". On an absent file it passes; on an existing file it refuses.
func TestWireNullFileSHA256DrivesAbsentAndStale(t *testing.T) {
	_, exp, _, _, err := parseConfigWriteArgs([]string{"summarize.models", "--workspace", "/ws", "--expect-state", `{"file_sha256":null,"values":{}}`})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := stateFileHash(exp.state.FileSHA256); !ok || got != "" {
		t.Fatalf("a wire null must decode to the absent value, got %q ok=%v", got, ok)
	}
	if err := checkExpectation(exp, "", false, map[string]any{}); err != nil {
		t.Fatalf("null on an absent file must pass: %v", err)
	}
	err = checkExpectation(exp, hashOf([]byte("present")), true, map[string]any{"summarize.models": []string{"pi:zeta"}})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrCoreConflict {
		t.Fatalf("null on an existing file must be a CORE-409 refusal, got %v", err)
	}
	// An omitted field is NOT an absent expectation: it carries no check.
	_, none, _, _, err := parseConfigWriteArgs([]string{"summarize.models", "--workspace", "/ws", "--expect-state", `{"values":{}}`})
	if err != nil {
		t.Fatal(err)
	}
	if expectsAbsent(none) {
		t.Fatal("an omitted file_sha256 is no expectation, not an absent one")
	}
}

// w22WorkspaceCtx builds a Context whose cli.toml maps one temp workspace and
// whose daemon socket is unreachable, so the workspace write path runs without
// a daemon (the candidate scope degrades to declared-only).
func w22WorkspaceCtx(t *testing.T) (ctx *Context, ws string) {
	t.Helper()
	ws = t.TempDir()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cli.toml")
	body := "server = \"http://127.0.0.1:1\"\nuser_id = \"usr_root\"\n\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PF_DAEMON_SOCKET", filepath.Join(dir, "absent.sock"))
	out := &bytes.Buffer{}
	return &Context{ConfigPath: cfgPath, FlagSocket: filepath.Join(dir, "absent.sock"), Stdout: out, Stderr: out}, ws
}

// TestWorkspaceCreateUnderWireNullExpectation drives the documented creation
// path from wire JSON through parseConfigWriteArgs: a mapped workspace with no
// yaml accepts a summarize write when the panel sends file_sha256 null.
func TestWorkspaceCreateUnderWireNullExpectation(t *testing.T) {
	ctx, ws := w22WorkspaceCtx(t)
	state := `{"file_sha256":null,"values":{}}`
	if err := runConfigSet(ctx, []string{"summarize.models", "pi:zeta", "--workspace", ws, "--expect-state", state}); err != nil {
		t.Fatalf("a wire-null expectation on an absent yaml must create it: %v", err)
	}
	raw, err := os.ReadFile(workspace.PathFor(ws))
	if err != nil {
		t.Fatalf("the yaml was not created: %v", err)
	}
	if !strings.Contains(string(raw), "models") {
		t.Fatalf("the created yaml does not carry the model list:\n%s", raw)
	}
}

// TestWorkspaceWireNullExpectationRefusesExisting is case (a) of the CAS
// bypass: a yaml created in a terminal after the panel's read must be refused,
// not silently overwritten.
func TestWorkspaceWireNullExpectationRefusesExisting(t *testing.T) {
	ctx, ws := w22WorkspaceCtx(t)
	yamlPath := workspace.PathFor(ws)
	orig := "summarize:\n  models: [\"pi:zeta\"]\n"
	if err := os.WriteFile(yamlPath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	state := `{"file_sha256":null,"values":{}}`
	err := runConfigSet(ctx, []string{"summarize.interval_sec", "10", "--workspace", ws, "--expect-state", state})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrCoreConflict {
		t.Fatalf("a wire-null expectation on an existing yaml must be CORE-409, got %v", err)
	}
	got, rerr := os.ReadFile(yamlPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != orig {
		t.Fatalf("the refused write touched the file:\n%s", got)
	}
}

// TestWorkspaceUnsetOnAbsentYamlWritesNothing pins the third B1 defect: an
// unset with no file to edit must create nothing — with no expectation and
// with a wire-null expectation alike.
func TestWorkspaceUnsetOnAbsentYamlWritesNothing(t *testing.T) {
	for name, expect := range map[string][]string{
		"no-expectation":   {"summarize.models", "--workspace", ""},
		"null-expectation": {"summarize.models", "--workspace", "", "--expect-state", `{"file_sha256":null,"values":{}}`},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, ws := w22WorkspaceCtx(t)
			args := append([]string{}, expect...)
			// fill the --workspace placeholder with the real temp dir
			for i, a := range args {
				if a == "" {
					args[i] = ws
				}
			}
			if err := runConfigUnset(ctx, args); err != nil {
				t.Fatalf("unset on an absent yaml must be a no-op: %v", err)
			}
			if _, err := os.Stat(workspace.PathFor(ws)); !os.IsNotExist(err) {
				t.Fatalf("unset on an absent yaml created %s (stat err %v)", workspace.PathFor(ws), err)
			}
		})
	}
}

func TestWorkspaceConfigSchemaUsesWorkspaceBounds(t *testing.T) {
	schema := WorkspaceConfigSchema("summarize.interval_sec")
	if schema["min"] != workspace.MinSummarizeIntervalSec || schema["max"] != workspace.MaxSummarizeIntervalSec {
		t.Fatalf("the interval schema must carry the workspace package's bounds, got %v", schema)
	}
	if WorkspaceConfigClass("summarize.interval_sec") != ConfigLive {
		t.Fatal("every summarize key is read live")
	}
}

func mustExpectState(t *testing.T, raw string) *configExpectState {
	t.Helper()
	var st configExpectState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	return &st
}
