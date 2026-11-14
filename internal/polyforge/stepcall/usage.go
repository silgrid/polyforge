package stepcall

import (
	"bufio"
	"encoding/json"
	"os"
)

type UsageSummary struct {
	Input, CacheRead, CacheWrite, Output int64
	ContextPeak                          int64
	Known                                bool
	IncludesSubagents                    bool
}
type usageValues struct{ input, read, write, output int64 }

func ReadUsage(paths []string, includesSubagents bool, start ...string) UsageSummary {
	since := ""
	if len(start) > 0 {
		since = start[0]
	}
	out := UsageSummary{IncludesSubagents: includesSubagents}
	seen := map[string]bool{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var rec struct {
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
				Message   struct {
					ID    string `json:"id"`
					Usage *struct {
						Input             int64 `json:"input"`
						Output            int64 `json:"output"`
						CacheRead         int64 `json:"cacheRead"`
						CacheWrite        int64 `json:"cacheWrite"`
						InputTokens       int64 `json:"input_tokens"`
						OutputTokens      int64 `json:"output_tokens"`
						CacheReadTokens   int64 `json:"cache_read_input_tokens"`
						CacheCreateTokens int64 `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(sc.Bytes(), &rec) != nil {
				continue
			}
			if since != "" && rec.Timestamp != "" && rec.Timestamp < since {
				continue
			}
			if rec.Type == "assistant" && rec.Message.Usage != nil {
				if rec.Message.ID != "" && seen[rec.Message.ID] {
					continue
				}
				if rec.Message.ID != "" {
					seen[rec.Message.ID] = true
				}
				u := rec.Message.Usage
				out.add(usageValues{u.InputTokens, u.CacheReadTokens, u.CacheCreateTokens, u.OutputTokens})
				continue
			}
			if rec.Type == "message" && rec.Message.Usage != nil {
				u := rec.Message.Usage
				out.add(usageValues{u.Input, u.CacheRead, u.CacheWrite, u.Output})
			}
		}
		_ = f.Close()
	}
	return out
}
func (s *UsageSummary) add(v usageValues) {
	if v.input == 0 && v.read == 0 && v.write == 0 && v.output == 0 {
		return
	}
	s.Known = true
	s.Input += v.input
	s.CacheRead += v.read
	s.CacheWrite += v.write
	s.Output += v.output
	if n := v.input + v.read + v.write; n > s.ContextPeak {
		s.ContextPeak = n
	}
}
