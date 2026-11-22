package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPiCompatibilityFailureNamesMissingFlag(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' '--no-skills --no-context-files --no-prompt-templates --no-extensions'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validatePiCompatibility(context.Background(), bin); err == nil {
		t.Fatal("compatibility check must reject a CLI missing --no-approve")
	}
}
