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
type usageRecord struct {
	Type    string `json:"type"`
	Message struct {
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
}

func ReadUsage(paths []string, includesSubagents bool) UsageSummary {
	out := UsageSummary{IncludesSubagents: includesSubagents}
	seen := map[string]bool{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		var lastPi *usageValues
		var claudeSeen bool
		var result *usageValues
		for sc.Scan() {
			var r usageRecord
			if json.Unmarshal(sc.Bytes(), &r) != nil {
				continue
			}
			if r.Type == "assistant" && r.Message.Usage != nil {
				claudeSeen = true
				if r.Message.ID != "" && seen[r.Message.ID] {
					continue
				}
				if r.Message.ID != "" {
					seen[r.Message.ID] = true
				}
				u := r.Message.Usage
				out.add(usageValues{u.InputTokens, u.CacheReadTokens, u.CacheCreateTokens, u.OutputTokens})
				continue
			}
			if r.Type == "result" && r.Usage != nil {
				u := r.Usage
				v := usageValues{u.InputTokens, u.CacheReadTokens, u.CacheCreateTokens, u.OutputTokens}
				result = &v
				continue
			}
			if r.Type == "message" && r.Message.Usage != nil {
				u := r.Message.Usage
				v := usageValues{u.Input, u.CacheRead, u.CacheWrite, u.Output}
				if lastPi != nil && *lastPi == v {
					continue
				}
				x := v
				lastPi = &x
				out.add(v)
				continue
			}
			if r.Usage != nil {
				u := r.Usage
				v := usageValues{u.Input, u.CacheRead, u.CacheWrite, u.Output}
				if v.input == 0 && v.read == 0 && v.write == 0 && v.output == 0 {
					v = usageValues{u.InputTokens, u.CacheReadTokens, u.CacheCreateTokens, u.OutputTokens}
				}
				if lastPi != nil && *lastPi == v {
					continue
				}
				x := v
				lastPi = &x
				out.add(v)
			}
		}
		if result != nil && !claudeSeen {
			out.add(*result)
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
