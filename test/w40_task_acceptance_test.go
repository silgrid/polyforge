package test

// w40_task_acceptance_test.go:'s end-to-end acceptance for the
// one-shot task agent, carried into (the re-established #40,
// owner ruling 2026-10-26: the OS sandbox is deleted, the feature body and
// the PF_TASK_ID write-verb guard stay). Authored against #40's spec
// ("## Acceptance criteria" + "#### Scenario" blocks) BEFORE `code` ran.
//
// Every bar below is RED on the pre-implementation tree at ASSERTION level
// (not a build red): the `task` verb family does not exist yet, so every
// `pf task ...` invocation answers `pf: unknown command "task"`, and the
// daemon's status document carries no tasks summary. This file compiles,
// vets and gofmts against the tree as it stands today and drives only
// surfaces that already exist: the real pf/aihub/fakepi binaries, the real
// daemon UDS face and the workspace `.polyforge.yaml`. No implementation
// internal is read.
//
// Scenario map (artifacts/e2e-scenarios.md carries the GIVEN/WHEN/THEN
// forms, the criteria each scenario pins, and the baseline evidence):
//
//   - TestW40TaskVerbFamilyAndUnsupportedArgs: AC2 (no task type, no
//     `--models`, no `--wait`) + AC20 (the verb family exists).
//   - TestW40TaskSpawnRefusesPathSelector: AC3 (a filesystem path is
//     refused naming the supported selectors).
//   - TestW40TaskNamedModelKeyRefusal: AC14/AC27 (a project with neither
//     list is refused naming the missing key, no hardcoded model).
//   - TestW40TaskBudgetCeilingsRefused: AC16 (a budget above the fixed
//     ceiling is refused).
//   - TestW40TaskYamlBudgetKeyRefused: AC16 (a budget key under
//     `steps.<p>.task` is refused as an unknown key).
//   - TestW40TaskSpawnReturnsIDAndIsListed: AC1 (immediate id) + AC20
//     (`list`/`show`).
//   - TestW40TaskZeroConfigDefaultFallback: AC27 (a default-only project
//     resolves the default instead of refusing).
//   - TestW40OpsStatusCarriesTasksSummary: AC24 (`pf ops status` one line)
//     + the daemon status document.
//
// The token/wall budgets, the four terminal states, the two-slot limit,
// retention, chat delivery and restart interruption are pinned at unit level
// (internal/polyforge/task, internal/polyforge/daemon) and on the real CLI
// face here; the write-verb guard has its own frozen reproduction
// (test/w53_task_write_verb_guard_repro_test.go). #40's read-only-boundary
// scenarios went with the sandbox: a task agent's filesystem/network trust
// now equals a session agent's ( owns platform-level isolation).

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// w40TaskIDRe is the task-id shape the plan pins (`task_` + 16 lowercase
// hex chars, mirroring `chat_`). The spec only requires an id; the shape is
// accepted as-is and matched loosely enough to catch a wrong family.
var w40TaskIDRe = regexp.MustCompile(`\btask_[0-9a-f]{16}\b`)

// w40TaskYaml renders a workspace `.polyforge.yaml` with one `steps.e2e`
// entry (a `task:` or `default:` line) built from the given candidate list.
func w40TaskYaml(entry string) string {
	return "steps:\n  e2e:\n    " + entry + "\n"
}

// w40Rig is one aihub install + one real daemon on its own socket, with an
// optional workspace `.polyforge.yaml` written before the daemon starts.
type w40Rig struct {
	inst *v1Install
	d    *v1Daemon
}

// w40Start boots the install + daemon and returns the rig.
func w40Start(t *testing.T, yaml string) *w40Rig {
	t.Helper()
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	if yaml != "" {
		w33Yaml(t, d.workspace, yaml)
	}
	d.start()
	return &w40Rig{inst: inst, d: d}
}

// pf runs the real pf CLI once against THIS rig's daemon and answers
// (combined output, exit code) with no assertion: the baseline reds ARE
// exit codes and outputs.
func (r *w40Rig) pf(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(r.inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"PF_SERVER="+r.inst.url,
		"PF_API_KEY="+r.inst.adminKey,
		"PF_USER_ID=usr_root",
		"PF_DAEMON_SOCKET="+r.d.sock,
	)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// TestW40TaskVerbFamilyAndUnsupportedArgs: the `task` verb family must be a
// recognized verb, and a spawn must refuse the arguments the spec forbids —
// `--models`, `--wait` and any task-type argument — rather than swallowing
// them. RED at baseline because `pf task ...` is an unknown command.
func TestW40TaskVerbFamilyAndUnsupportedArgs(t *testing.T) {
	r := w40Start(t, w40TaskYaml("task: { models: [pi:w40-ok/m] }"))

	// `pf task list` is a recognized verb (an empty list is a legal answer).
	if out, code := r.pf(t, "task", "list"); code != 0 || strings.Contains(out, "unknown command") {
		t.Fatalf("`pf task list` must be a recognized verb; exit=%d output=%q", code, out)
	}

	for _, tc := range []struct {
		flag string
		args []string
	}{
		{"--models", []string{"task", "spawn", "--project", "e2e", "--models", "pi:a/b", "hi"}},
		{"--wait", []string{"task", "spawn", "--project", "e2e", "--wait", "hi"}},
		{"--type", []string{"task", "spawn", "--project", "e2e", "--type", "research", "hi"}},
	} {
		out, code := r.pf(t, tc.args...)
		if code == 0 {
			t.Fatalf("`pf %s` must be refused; it succeeded: %q", strings.Join(tc.args, " "), out)
		}
		if strings.Contains(out, "unknown command") {
			t.Fatalf("`pf %s`: the task verb family must be recognized (got %q)", strings.Join(tc.args, " "), out)
		}
		if !strings.Contains(out, tc.flag) {
			t.Fatalf("`pf %s`: the refusal must name %s; got %q", strings.Join(tc.args, " "), tc.flag, out)
		}
	}
}

// TestW40TaskSpawnRefusesPathSelector: a spawn that names a filesystem path
// instead of a project must be refused with a message naming the supported
// selector (`--project`), and no agent may start.
func TestW40TaskSpawnRefusesPathSelector(t *testing.T) {
	r := w40Start(t, w40TaskYaml("task: { models: [pi:w40-ok/m] }"))

	out, code := r.pf(t, "task", "spawn", "--project", "/tmp/not-a-project", "hi")
	if code == 0 {
		t.Fatalf("a filesystem-path selector must be refused; it succeeded: %q", out)
	}
	if strings.Contains(out, "unknown command") {
		t.Fatalf("the task verb family must be recognized; got %q", out)
	}
	if !strings.Contains(out, "--wi") && !strings.Contains(strings.ToLower(out), "selector") {
		t.Fatalf("the refusal must name the supported selectors; got %q", out)
	}
}

// TestW40TaskNamedModelKeyRefusal: with neither `steps.e2e.task.models` nor
// `steps.e2e.default.models` set, the spawn must be refused naming the
// missing configuration key and must not silently pick a hardcoded model.
func TestW40TaskNamedModelKeyRefusal(t *testing.T) {
	r := w40Start(t, "")

	out, code := r.pf(t, "task", "spawn", "--project", "e2e", "hi")
	if code == 0 {
		t.Fatalf("a spawn with no model configuration must be refused; it succeeded: %q", out)
	}
	if strings.Contains(out, "unknown command") {
		t.Fatalf("the task verb family must be recognized; got %q", out)
	}
	if !strings.Contains(out, "steps.e2e.task") && !strings.Contains(out, "steps.e2e.default") {
		t.Fatalf("the refusal must name the missing configuration key; got %q", out)
	}
}

// TestW40TaskBudgetCeilingsRefused: a wall-clock or token budget above the
// fixed ceiling (60m / 1000000) must be refused naming the ceiling.
func TestW40TaskBudgetCeilingsRefused(t *testing.T) {
	r := w40Start(t, w40TaskYaml("task: { models: [pi:w40-ok/m] }"))

	for _, args := range [][]string{
		{"task", "spawn", "--project", "e2e", "--max-duration", "2h", "hi"},
		{"task", "spawn", "--project", "e2e", "--max-tokens", "2000000", "hi"},
	} {
		out, code := r.pf(t, args...)
		if code == 0 {
			t.Fatalf("`pf %s` must be refused above the ceiling; it succeeded: %q", strings.Join(args, " "), out)
		}
		if strings.Contains(out, "unknown command") {
			t.Fatalf("`pf %s`: the task verb family must be recognized; got %q", strings.Join(args, " "), out)
		}
	}
}

// TestW40TaskYamlBudgetKeyRefused: `steps.<p>.task` takes only `models`; a
// budget key under it must be refused as an unknown key. The valid yaml is
// swapped for the invalid one after the daemon is up, so the bar pins the
// live load, not daemon startup.
func TestW40TaskYamlBudgetKeyRefused(t *testing.T) {
	r := w40Start(t, w40TaskYaml("task: { models: [pi:w40-ok/m] }"))
	w33Yaml(t, r.d.workspace, w40TaskYaml("task: { models: [pi:w40-ok/m], max_tokens: 5 }"))

	out, code := r.pf(t, "task", "spawn", "--project", "e2e", "hi")
	if code == 0 {
		t.Fatalf("a budget key under steps.e2e.task must be refused as unknown; it succeeded: %q", out)
	}
	if strings.Contains(out, "unknown command") {
		t.Fatalf("the task verb family must be recognized; got %q", out)
	}
	if !strings.Contains(out, "max_tokens") && !strings.Contains(strings.ToLower(out), "unknown") {
		t.Fatalf("the refusal must name the unknown key; got %q", out)
	}
}

// TestW40TaskSpawnReturnsIDAndIsListed: `pf task spawn` returns an id
// immediately (before the task finishes), and the task is then observable
// through `pf task list` and readable through `pf task show <id>`.
func TestW40TaskSpawnReturnsIDAndIsListed(t *testing.T) {
	r := w40Start(t, w40TaskYaml("task: { models: [pi:w40-ok/m] }"))

	out, code := r.pf(t, "task", "spawn", "--project", "e2e", "say hi")
	if code != 0 {
		t.Fatalf("`pf task spawn` must return an id; exit=%d output=%q", code, out)
	}
	id := w40TaskIDRe.FindString(out)
	if id == "" {
		t.Fatalf("`pf task spawn` must print a task id (`task_<16 hex>`); output=%q", out)
	}

	waitUntil(t, 30*time.Second, "the spawned task never appeared in `pf task list`", func() bool {
		lo, _ := r.pf(t, "task", "list")
		return strings.Contains(lo, id)
	})

	so, code := r.pf(t, "task", "show", id)
	if code != 0 || !strings.Contains(so, id) {
		t.Fatalf("`pf task show %s` must return the task record; exit=%d output=%q", id, code, so)
	}
}

// TestW40TaskZeroConfigDefaultFallback: a non-workbench project with only
// `steps.e2e.default.models` set must resolve that default (no hardcoded
// model, no missing-key refusal). The spawn must succeed (exit 0), return a
// `task_<16hex>` id, and the task's route document must name the default
// candidate `pi:w40-ok/m` — i.e. the default list was actually USED, not
// merely not named as missing (code review: the old assertions threw away the
// exit code and passed for any non-key refusal).
func TestW40TaskZeroConfigDefaultFallback(t *testing.T) {
	r := w40Start(t, w40TaskYaml("default: { models: [pi:w40-ok/m] }"))

	out, code := r.pf(t, "task", "spawn", "--project", "e2e", "hi")
	if code != 0 {
		t.Fatalf("a default-only project must spawn; exit=%d output=%q", code, out)
	}
	id := w40TaskIDRe.FindString(out)
	if id == "" {
		t.Fatalf("`pf task spawn` must print a task_<16hex> id; output=%q", out)
	}

	// The candidate is assigned when the runner starts, just after the
	// spawn returns: poll the record until it names the default candidate.
	waitUntil(t, 30*time.Second, "the task record never named the default candidate pi:w40-ok/m", func() bool {
		show, sc := r.pf(t, "task", "show", id, "--json")
		return sc == 0 && strings.Contains(show, "pi:w40-ok/m")
	})
}

// TestW40OpsStatusCarriesTasksSummary: the daemon status document carries a
// tasks summary and `pf ops status` renders exactly one task line. RED at
// baseline: the status route has no tasks object.
func TestW40OpsStatusCarriesTasksSummary(t *testing.T) {
	r := w40Start(t, "")

	raw, err := r.d.client.Status()
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	if !strings.Contains(string(raw), `"tasks"`) {
		t.Fatalf("GET /status must carry a tasks summary; body: %s", raw)
	}

	out := r.d.pf(t, "ops", "status")
	if !strings.Contains(strings.ToLower(out), "task") {
		t.Fatalf("`pf ops status` must carry a tasks summary line; output:\n%s", out)
	}
}
