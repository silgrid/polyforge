package daemon

// analysis_components.go (polyforge#89, AC3): the component division
// the pre-filing analysis runs on. The daemon — the one side that knows
// the lane's shared scripts (its WorkflowsDir) and the mapped clones —
// runs the workflow's components.py over the ticket's repositories
// (its frozen repo_inventory, else the project's every repository —
// the same list a claim clones, claimRepoNames) and attaches the tool's
// line report to the analysis prompt between the template and the
// ticket: the analysis agent has Bash but may not reach the release
// directory, and a prompt-borne division is deterministic and testable.
// Failure is never a refusal: a missing clone, an unusable tool, a
// failing run or a timeout answers one fallback line (the template's
// defaults apply) and the dispatch always proceeds — the tool runs at
// dispatch time, inside the analysis dispatch walk's lock, so it is
// bounded and its whole process group is killed at the deadline
// (polyforge#72's lesson: nothing unbounded runs at dispatch time).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// analysisComponentsTimeout bounds one components.py run (the doctor's
// probe bound is the precedent; this one is smaller: the report is a
// nicety the analysis can fall back from, never worth half a minute of
// dispatch).
var analysisComponentsTimeout = 10 * time.Second

// analysisComponentsKillDelay is the grace the stuck tool's process
// group gets after the deadline before it is killed hard.
const analysisComponentsKillDelay = time.Second

// analysisComponentsSection answers the prompt section that carries the
// ticket repositories' component division: the tool's line report under
// a one-line intro, or — for every repository it could not be computed
// for — one fallback line naming why (the template's defaults apply);
// "" when the ticket names no repository at all. The section never
// fails and never blocks the dispatch.
func (d *Daemon) analysisComponentsSection(item *api.WorkItem, ws string) string {
	names := d.claimRepoNames(item.ID, item.ProjectName)
	if len(names) == 0 {
		return ""
	}
	var clones, missing []string
	for _, name := range names {
		if dir := workspace.RepoCloneDir(ws, name); workspace.IsRepoClone(dir) {
			clones = append(clones, dir)
		} else {
			missing = append(missing, name)
		}
	}
	var b strings.Builder
	if len(missing) > 0 {
		fmt.Fprintf(&b, "组件划分：仓库 %s 在工作区没有 clone——按默认阈值判断。\n", strings.Join(missing, "、"))
	}
	if len(clones) == 0 {
		return b.String()
	}
	tool := filepath.Join(d.cfg.WorkflowsDir, "_shared", "scripts", "components.py")
	if _, err := os.Stat(tool); err != nil {
		b.WriteString("组件划分未附带（工具不可用）——按默认阈值判断。\n")
		return b.String()
	}
	argv := []string{"python3", tool}
	for _, clone := range clones {
		argv = append(argv, "--repo", clone)
	}
	var out bytes.Buffer
	if err := runAnalysisTool(analysisComponentsTimeout, ws, argv, &out); err != nil {
		reason := "工具失败"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "工具超时"
		}
		fmt.Fprintf(&b, "组件划分未附带（%s）——按默认阈值判断。\n", reason)
		return b.String()
	}
	report := strings.Trim(out.String(), "\n")
	if report == "" {
		b.WriteString("组件划分未附带（工具无输出）——按默认阈值判断。\n")
		return b.String()
	}
	b.WriteString("各仓库组件划分与生效阈值（工具对本票各仓库的输出，升级 epic 建议按它判断）：\n\n")
	b.WriteString(report + "\n")
	return b.String()
}

// runAnalysisTool runs argv in dir with stdin closed, bounded at the
// timeout: the whole process group is killed at the deadline (the
// doctor's runBounded shape); a timeout answers an error wrapping
// context.DeadlineExceeded.
func runAnalysisTool(timeout time.Duration, dir string, argv []string, stdout *bytes.Buffer) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = nil // the report is the answer; the tool's diagnostics are its own
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = analysisComponentsKillDelay
	err := cmd.Run()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", context.DeadlineExceeded, err)
	}
	return err
}
