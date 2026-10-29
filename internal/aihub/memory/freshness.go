package memory

// freshness.go: the freshness term of search ordering. Text relevance
// alone ranks a year-old note level with yesterday's; recall should
// prefer the fresher of two comparable matches without letting age bury
// a clearly better one.
//
// Per entry, with age_days = now - memories.updated_at (the stored
// document's last revision — memories have no update endpoint, so it
// equals created_at):
//
//	w     = exp(-age_days / stability_days)       ∈ [0, 1]; 1 for a non-decaying type
//	score = rank × (1 + freshnessBoost × w)       (rank: FTS5 bm25, negative = better)
//
// ordered ascending (most negative first). The factor lies in
// [1, 1+freshnessBoost], so freshness can scale a match's relevance by
// at most 1.5×: a fresh entry wins a near-tie, and an old match more than
// 1.5× as relevant as a fresh one always stays ahead of it. The
// multiplicative form is independent of bm25's absolute scale, which
// varies with corpus size and query length. Exact ties fall back to
// updated_at DESC compared as instants (whole-second and fractional
// RFC3339 stamps both occur; an unparseable one counts as oldest), then
// id. The substring-scan path (short tokens only,
// no rank) orders by w alone. ftsPage reads every match's rank and row
// meta, but never a stored document; see there.

import (
	"math"
	"strings"
	"time"
)

// freshnessBoost is the largest relative lift freshness gives a match.
const freshnessBoost = 0.5

// typeStability maps a type family to its stability in days (0 = never
// decays); the first entry naming the type or one of its dotted
// ancestors wins. rule.* and the standing doc kinds (doc.design,
// doc.decision) are references that stay current until redacted;
// doc.review, doc.audit and other doc.* are point-in-time snapshots;
// experience.* is tied to circumstances that change; fact.* and
// methodology.* hold longer. Any other type uses defaultStabilityDays.
var typeStability = []struct {
	family string
	days   float64
}{
	{"rule", 0},
	{"doc.design", 0},
	{"doc.decision", 0},
	{"doc", 180},
	{"experience", 90},
	{"fact", 180},
	{"methodology", 180},
}

const defaultStabilityDays = 90

// stabilityDays answers a type's stability (0 = never decays).
func stabilityDays(typ string) float64 {
	for _, ts := range typeStability {
		if typ == ts.family || strings.HasPrefix(typ, ts.family+".") {
			return ts.days
		}
	}
	return defaultStabilityDays
}

// parseUpdatedAt reads an updated_at value. It is written RFC3339
// (core.Now, or a frontmatter timestamp), with or without fractional
// seconds; one that does not parse answers the zero time — the oldest
// instant, and fully stale for freshness.
func parseUpdatedAt(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// freshness answers w for one entry at query time now (updated from
// parseUpdatedAt). An unparseable updated_at counts as fully stale
// (w = 0). A future one counts as age 0, so w never exceeds 1.
func freshness(typ string, updated, now time.Time) float64 {
	days := stabilityDays(typ)
	if days == 0 {
		return 1
	}
	if updated.IsZero() {
		return 0
	}
	age := max(0, now.Sub(updated).Hours()/24)
	return math.Exp(-age / days)
}
