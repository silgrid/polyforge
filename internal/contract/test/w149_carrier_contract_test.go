package test

// w149_carrier_contract_test.go: the repo-file reader the text contracts
// share. Its own contract (the plan steps' deliverable
// carrier duty) went with the plan steps of the lanes
// removed, and its fragment (deliverable_carrier, which no shipped step
// included and which still taught the removed needs_input) with workflow
// contract 3.

import (
	"os"
	"path/filepath"
	"testing"
)

func w149CarrierRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}
