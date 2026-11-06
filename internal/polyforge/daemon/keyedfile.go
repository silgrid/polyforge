package daemon

// keyedfile.go: the daemon's keyed filing, through its own credentials:
// fileKeyed files a work item on a lane, not submitted, idempotent by the
// ticket's key (a line of its body), looked up before filing — a step's
// pending `pf step hub file` (stepHubSink, stephub.go). Nothing here
// submits what it files.
//
// Reachability: daemon code only. No daemon route serves the writes
// directly; a step reaches them only as the pending writes of its
// `pf step hub` calls, made by the daemon when its round hands off done
// (a step process holds no aihub key and no machine key).

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// ticketKeyLine is the line a filed ticket's body carries its key in. It
// is a stored marker, not a lane concept: a data format already written
// into the content of existing tickets, by which fileKeyed finds a filed
// ticket again. Its literal stays as it is — renamed, the next batch would
// re-file duplicate fix tickets.
func ticketKeyLine(key string) string { return "integration-ticket-key: " + key }

// fileKeyed files a work item of project on lane (not submitted),
// idempotent by key: a work item of the project on that lane whose body
// carries the key's line (ticketKeyLine) is answered instead of filing
// another; the filed body carries the line. An empty key files
// unconditionally. A step's pending `pf step hub file` (stepHubSink)
// files through it.
func fileKeyed(c *aihubclient.Client, project, lane, key, goal, content, priority string, labels []string) (string, error) {
	bundle := lane
	if key != "" {
		line := ticketKeyLine(key)
		items, err := c.ListWorkItems(aihubclient.WorkItemFilter{Project: project, Bundle: bundle, Limit: 1000})
		if err != nil {
			return "", fmt.Errorf("looking up the ticket %s before filing it: %w", key, err)
		}
		for _, it := range items {
			if it.Content != nil && strings.Contains(*it.Content, line) {
				return api.DisplayName(it.Slug, it.ID), nil
			}
		}
		content = strings.TrimRight(content, "\n") + "\n\n" + line + "\n"
	}
	raw, err := c.CreateWorkItem(aihubclient.CreateWorkItemRequest{Project: project, Goal: goal, Content: &content,
		Priority: priority, Labels: labels, BundleName: &bundle})
	if err != nil {
		return "", err
	}
	var wi struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil || wi.ID == "" {
		return "", fmt.Errorf("the created ticket's answer is unreadable: %v", err)
	}
	return api.DisplayName(wi.Slug, wi.ID), nil
}
