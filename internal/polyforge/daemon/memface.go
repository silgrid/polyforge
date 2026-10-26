package daemon

// memface.go: the F6-A claim-time memory adapter — the daemon-side half
// of the recalled-memories snapshot. The aihub call is made HERE, with
// the daemon's own machine credential: the spawned agents hold no aihub
// credential, no aihub URL and no direct server path (the hard
// constraint of the F6 ruling). The UDS recall/remember proxies (F6-B)
// were retired with the WI-Agent lane (polyforge#106).
//
// The wire shape is the aihub memory search endpoint's own (with the F6
// excerpt extension — a bounded content prefix per hit), behind the
// generic aihubclient.Do so the shared client stays untouched.

import (
	"net/url"
	"strconv"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// snapshotExcerptRunes bounds each hit's excerpt: the static snapshot
// carries short prefixes (it is one of many capsule inputs), under the
// server's excerpt ceiling.
const snapshotExcerptRunes = 1200

// memSearchHit decodes one search answer row with the excerpt extension.
type memSearchHit struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	Title          *string `json:"title"`
	ContentExcerpt string  `json:"content_excerpt"`
}

func (h memSearchHit) title() string {
	if h.Title != nil {
		return *h.Title
	}
	return ""
}

// recallSnapshot binds the F6-A claim-time seam: goal+content (and the
// work item's own project, so the search is scoped to exactly that
// project, never a search over every project the machine credential
// can read) in, the top-K hits (identity + bounded excerpt) out. The
// runtime's materialize writes the answer as the static snapshot;
// failures travel back as errors and degrade to a noticed empty list
// there.
func recallSnapshot(hub Hub) func(query, project string, topK int) ([]runtime.RecalledMemory, error) {
	return func(query, project string, topK int) ([]runtime.RecalledMemory, error) {
		hits, err := memSearch(hub, query, project, topK, snapshotExcerptRunes)
		if err != nil {
			return nil, err
		}
		out := make([]runtime.RecalledMemory, 0, len(hits))
		for _, h := range hits {
			out = append(out, runtime.RecalledMemory{
				ID:      h.ID,
				Type:    h.Type,
				Title:   h.title(),
				Excerpt: h.ContentExcerpt,
			})
		}
		return out, nil
	}
}

// memSearch runs one bounded memory search against aihub and decodes the
// excerpted hits. project, when non-empty, scopes the search to that
// project (aihub resolves ids and names alike).
func memSearch(hub Hub, query, project string, topK, excerptRunes int) ([]memSearchHit, error) {
	v := url.Values{}
	v.Set("q", query)
	// Ranked any-term mode: the claim query is a whole goal, which the
	// default every-token-must-match search would rarely answer. An
	// aihub that predates match= ignores it and ANDs (zero hits — the
	// snapshot degrades to its noticed empty list), so aihub deploys
	// first.
	v.Set("match", "any")
	if project != "" {
		v.Set("project", project)
	}
	if topK > 0 {
		v.Set("limit", strconv.Itoa(topK))
	}
	v.Set("excerpt", strconv.Itoa(excerptRunes))
	var resp struct {
		Items []memSearchHit `json:"items"`
	}
	if err := hub.Do("GET", "/api/v1/memories/search?"+v.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}
