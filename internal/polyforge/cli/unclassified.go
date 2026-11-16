package cli

// unclassified.go (polyforge#51): the wire shapes of the daemon's
// unclassified-error list — GET /ops/unclassified's answer, the payload
// `pf --json ops unclassified` re-emits and Pi Web's Host region reads.
// The report lists the texts that fell into the classification FALLBACK
// over the recent window, deduplicated by the raw error text with a count
// each, aggregated across every serving Cell of the Host.

import "time"

// UnclassifiedWindow is the list's look-back window (days).
const UnclassifiedWindow = 7 * 24 * time.Hour

// UnclassifiedItem is one deduplicated fallback text with its occurrences.
type UnclassifiedItem struct {
	Text   string `json:"text"`
	Count  int    `json:"count"`
	LastAt string `json:"last_at"`
}

// UnclassifiedReport is the Host-aggregated answer of GET /ops/unclassified.
type UnclassifiedReport struct {
	WindowDays int                `json:"window_days"`
	Items      []UnclassifiedItem `json:"items"`
}
