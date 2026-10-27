package daemon

// analyzer.go: the explicit, human-triggered, read-only message analyzer
// (workbench#11). It reuses the narration lane's one-shot dispatcher
// (summarize.ExecDispatcher: stdin prompt, summarize-cwd, 5-minute bound),
// runs one at a time outside max_concurrent_jobs, writes nothing to a
// capsule/repo/aihub, and answers the conclusion only to the caller.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/summarize"
)

// analyzerWallClock is the single cap across every model attempt of one
// analyzer run (the summarize models are tried in order inside this one
// budget, never one 300 s timeout each).
const analyzerWallClock = 300 * time.Second

func (d *Daemon) analyzeRoute(w http.ResponseWriter, r *http.Request) {
	d.analyzerMu.Lock()
	if d.analyzerBusy {
		d.analyzerMu.Unlock()
		writeErr(w, api.ErrDaemonConflict, "an analyzer run is already in progress on this daemon")
		return
	}
	d.analyzerBusy = true
	d.analyzerMu.Unlock()
	defer func() {
		d.analyzerMu.Lock()
		d.analyzerBusy = false
		d.analyzerMu.Unlock()
	}()

	id := r.PathValue("id")
	var msg api.Message
	if err := d.hub.Do("GET", "/api/v1/messages/"+id, nil, &msg); err != nil {
		writeErrOf(w, err)
		return
	}
	var wi *api.WorkItem
	if msg.AboutWI != "" {
		if it, err := d.hub.GetWorkItem(msg.AboutWI); err == nil {
			wi = it
		}
	}
	var reply *api.Message
	if msg.ReplyTo != "" {
		var rm api.Message
		if err := d.hub.Do("GET", "/api/v1/messages/"+msg.ReplyTo, nil, &rm); err == nil {
			reply = &rm
		}
	}
	project := ""
	if wi != nil {
		project = wi.ProjectName
	}
	if project == "" {
		if projects := d.cfg.Mapping.Projects(); len(projects) > 0 {
			project = projects[0]
		}
	}
	models, _, _ := d.rt.SummarizeLane(project)
	if len(models) == 0 {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("no summarize.models configured for project %q; the analyzer has no budgeted model", project))
		return
	}
	if d.analyzerDispatch == nil {
		writeErr(w, api.ErrDaemonInternal, "analyzer dispatcher is not assembled")
		return
	}
	prompt := analyzerPrompt(msg, wi, reply)
	ctx, cancel := context.WithTimeout(r.Context(), analyzerWallClock)
	defer cancel()
	out, model, err := runAnalyzer(ctx, d.analyzerDispatch, models, prompt)
	if err != nil {
		writeErrOf(w, &api.Error{Code: api.ErrDaemonInternal, Detail: fmt.Sprintf("analyzer run failed within its %s budget: %v", analyzerWallClock, err)})
		return
	}
	writeJSON(w, http.StatusOK, api.AnalyzeResult{MessageID: msg.ID, Conclusion: strings.TrimSpace(out), Model: model})
}

// runAnalyzer walks the models in order within ONE caller-owned deadline:
// each dispatch gets only the remaining budget, and the walk stops once
// the deadline passes instead of granting every candidate its own timeout.
func runAnalyzer(ctx context.Context, dispatch summarize.Dispatcher, models []string, prompt string) (string, string, error) {
	var lastErr error
	for _, model := range models {
		if ctx.Err() != nil {
			break
		}
		var out string
		var err error
		if cd, ok := dispatch.(summarize.ContextDispatcher); ok {
			out, err = cd.DispatchContext(ctx, model, prompt)
		} else {
			out, err = dispatch.Dispatch(model, prompt)
		}
		if err == nil {
			return out, model, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = ctx.Err()
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no summarize.models candidate")
	}
	return "", "", lastErr
}

func analyzerPrompt(msg api.Message, wi *api.WorkItem, reply *api.Message) string {
	var b strings.Builder
	b.WriteString("You are analyzing one user-to-user message. Read-only: answer with the conclusion only.\n\n")
	fmt.Fprintf(&b, "message: %s (seq %d, conversation %s)\nfrom: %s\nat: %s\n", msg.ID, msg.Seq, msg.ConversationID, msg.SenderID, msg.CreatedAt)
	if msg.AboutWI != "" {
		fmt.Fprintf(&b, "about work item: %s\n", msg.AboutWI)
	}
	b.WriteString("\ntext:\n")
	b.WriteString(msg.Text)
	b.WriteString("\n")
	if wi != nil {
		fmt.Fprintf(&b, "\nanchored work item: %s (%s), status %s\ngoal: %s\n", wi.Slug, wi.ID, wi.Status, wi.Goal)
	}
	if reply != nil {
		fmt.Fprintf(&b, "\nreplied-to message %s from %s:\n%s\n", reply.ID, reply.SenderID, reply.Text)
	}
	b.WriteString("\nGive a concise, concrete analysis and the recommended next action.\n")
	return b.String()
}
