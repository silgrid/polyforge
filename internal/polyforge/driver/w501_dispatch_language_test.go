package driver

// (AC5): a dispatch's agent language is fixed when it is
// dispatched. A new conversation takes the live `[daemon] agent_language`;
// a dispatch that resumes the previous conversation keeps that
// conversation's language (it already holds its prompt). Each dispatch
// records its language on its spawn-journal line.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
)

func TestW501DispatchLanguageFixedAtDispatch(t *testing.T) {
	ad, sp, _ := spawnEnv(t)
	live := "en"
	ad.Language = func() string { return live }

	// 1. a first dispatch under `en`: the English sentence, journal `en`
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", SpawnN: 1, Round: 1, Harness: harness.Pi}); err != nil {
		t.Fatalf("SpawnStep grill#1: %v", err)
	}
	p1, _ := sp.last()
	assertLanguageSentence(t, p1.InitialPrompt, c4LanguageEN, c4LanguageZhCN)
	if err := p1.Journal(101, nowStampX()); err != nil {
		t.Fatalf("journal grill#1: %v", err)
	}
	if e := lastJournal(t, ad.WI.CapsuleDir); e.Language != "en" {
		t.Fatalf("the first dispatch's journal line records language %q, want en", e.Language)
	}
	// its conversation reached disk: a resume can continue it (the
	// capture spawner creates no session directory; the harness would)
	if err := os.MkdirAll(filepath.Dir(p1.TranscriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p1.TranscriptPath, []byte(`{"t":"turn"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. the owner switches the language while the step waits on its ask
	live = "zh-CN"

	// 3. the answer resumes that conversation: it keeps `en`
	reply := capsule.AskLine{At: "2026-11-12T10:00:00.000000001Z", Step: "grill", Spawn: 1, Kind: capsule.AskKindReply, Text: "lean", By: "owner"}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "grill", SpawnN: 2, Round: 1, Harness: harness.Pi,
		Resume: true, MessageResume: true, OwnerMessages: []capsule.AskLine{reply}}); err != nil {
		t.Fatalf("SpawnStep grill#2 (resume): %v", err)
	}
	p2, _ := sp.last()
	if !p2.Resume || p2.TranscriptPath != p1.TranscriptPath {
		t.Fatalf("the answer must resume the first conversation: resume=%v transcript=%q", p2.Resume, p2.TranscriptPath)
	}
	if err := p2.Journal(102, nowStampX()); err != nil {
		t.Fatalf("journal grill#2: %v", err)
	}
	if e := lastJournal(t, ad.WI.CapsuleDir); e.SpawnN != 2 || e.Language != "en" {
		t.Fatalf("the resumed dispatch's journal line: spawn %d language %q, want spawn 2 language en (its conversation's)", e.SpawnN, e.Language)
	}

	// 4. the next step is a new conversation: it takes `zh-CN`
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "fix", SpawnN: 3, Round: 1, Harness: harness.Pi}); err != nil {
		t.Fatalf("SpawnStep fix#3: %v", err)
	}
	p3, _ := sp.last()
	assertLanguageSentence(t, p3.InitialPrompt, c4LanguageZhCN, c4LanguageEN)
	if err := p3.Journal(103, nowStampX()); err != nil {
		t.Fatalf("journal fix#3: %v", err)
	}
	if e := lastJournal(t, ad.WI.CapsuleDir); e.SpawnN != 3 || e.Language != "zh-CN" {
		t.Fatalf("the new dispatch's journal line: spawn %d language %q, want spawn 3 language zh-CN", e.SpawnN, e.Language)
	}
	if strings.Contains(p2.InitialPrompt, c4LanguageZhCN) {
		t.Fatal("the resumed conversation must not be told the new language")
	}
}
