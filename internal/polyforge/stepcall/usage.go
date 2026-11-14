package stepcall

// UsageSummary is token accounting for one handoff's session records.
type UsageSummary struct {
	Input, CacheRead, CacheWrite, Output int64
	ContextPeak int64
	Known bool
	IncludesSubagents bool
}

// ReadUsage reads harness JSONL transcripts. It returns Unknown when no usable usage exists.
func ReadUsage(paths []string, includesSubagents bool) UsageSummary {
	return UsageSummary{IncludesSubagents: includesSubagents}
}
