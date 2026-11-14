package liveconfig

import (
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

func (s *State) SetAgentLanguageFallback(func() string) {}

func TestAgentLanguageUsesDefaultCellUntilNamedCellOverrides(t *testing.T) {
	def := New(&mapping.Plan{}, "", 0, 0)
	named := New(&mapping.Plan{}, "", 0, 0)
	named.SetAgentLanguageFallback(def.AgentLanguage)
	if got := named.AgentLanguage(); got != "en" {
		t.Fatalf("both cells unset: got %q, want en", got)
	}
	def.SetAgentLanguage("zh-CN")
	if got := named.AgentLanguage(); got != "zh-CN" {
		t.Fatalf("named cell inherited language: got %q, want zh-CN", got)
	}
	named.SetAgentLanguage("en")
	def.SetAgentLanguage("zh-CN")
	if got := named.AgentLanguage(); got != "en" {
		t.Fatalf("named cell explicit language: got %q, want en", got)
	}
	named.SetAgentLanguage("")
	def.SetAgentLanguage("en")
	if got := named.AgentLanguage(); got != "en" {
		t.Fatalf("named cell follows changed default: got %q, want en", got)
	}
}
