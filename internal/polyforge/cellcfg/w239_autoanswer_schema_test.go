package cellcfg

// w239_autoanswer_schema_test.go: the automatic reply's two keys are Cell
// keys applied live (polyforge#239) — never Host-only, never restart-only.

import "testing"

func TestAutoAnswerKeysAreCellLive(t *testing.T) {
	for _, name := range []string{"ask_auto_reply_enabled", "ask_auto_reply_timeout"} {
		k, ok := Lookup("daemon", name)
		if !ok {
			t.Fatalf("%s is not in the Host/Cell table", name)
		}
		if k.Scope != ScopeCell || k.Class != ClassLive {
			t.Fatalf("%s = %+v, want Cell/live", name, k)
		}
	}
}
