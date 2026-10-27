package cli

import (
	"strings"
	"testing"
)

func TestValidateConfigForDaemon(t *testing.T) {
	clearEnv(t)
	ws := t.TempDir()
	ok := "server = \"http://127.0.0.1:1\"\n\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	cfg, err := ParseConfigBytes("/tmp/cli.toml", []byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigForDaemon(cfg); err != nil {
		t.Fatalf("a valid config was refused: %v", err)
	}

	empty, err := ParseConfigBytes("/tmp/cli.toml", []byte("server = \"http://x\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigForDaemon(empty); err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Fatalf("an empty mapping must be refused naming it, got %v", err)
	}

	missing := t.TempDir() + "/does-not-exist"
	bad, err := ParseConfigBytes("/tmp/cli.toml", []byte("[workspace.\""+missing+"\"]\nprojects = [\"e2e\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigForDaemon(bad); err == nil {
		t.Fatal("a missing workspace must be refused")
	}

	daemonBad, err := ParseConfigBytes("/tmp/cli.toml", []byte(ok+"\n[daemon]\nmax_concurrent_jobs = 0\n"))
	if err == nil {
		t.Fatal("cap 0 must be a parse refusal")
	}
	if !strings.Contains(err.Error(), "max_concurrent_jobs") {
		t.Fatalf("the refusal must name the key, got %v", err)
	}
	_ = daemonBad
}

func TestParseConfigBytesRoundTripsDurations(t *testing.T) {
	clearEnv(t)
	ws := t.TempDir()
	text := "[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n\n[daemon]\nstep_timeout = \"45m\"\nstall_window = \"2m\"\n"
	cfg, err := ParseConfigBytes("/tmp/cli.toml", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Daemon.StepTimeout.String() != "45m0s" || cfg.Daemon.StallWindow.String() != "2m0s" {
		t.Fatalf("durations = %v / %v", cfg.Daemon.StepTimeout, cfg.Daemon.StallWindow)
	}
}

// TestValidateConfigWriteMappingScope pins the owner's A1 scoping: the
// mapping-non-empty/usable checks apply only to a write that touches the
// mapping or leaves one in place; a flat/[daemon] write to a mapping-less
// file is accepted.
func TestValidateConfigWriteMappingScope(t *testing.T) {
	clearEnv(t)
	empty, err := ParseConfigBytes("/tmp/cli.toml", []byte("server = \"http://x\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigWrite(empty, false); err != nil {
		t.Fatalf("a flat write to a mapping-less file must be accepted: %v", err)
	}
	if err := ValidateConfigWrite(empty, true); err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Fatalf("a mapping-touching write to an empty mapping must refuse naming it, got %v", err)
	}

	ws := t.TempDir()
	mapped, err := ParseConfigBytes("/tmp/cli.toml", []byte("[workspace.\""+ws+"\"]\nprojects = [\"e2e\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigWrite(mapped, false); err != nil {
		t.Fatalf("a flat write leaving a usable mapping in place must be accepted: %v", err)
	}

	missing := t.TempDir() + "/does-not-exist"
	bad, err := ParseConfigBytes("/tmp/cli.toml", []byte("[workspace.\""+missing+"\"]\nprojects = [\"e2e\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigWrite(bad, false); err == nil {
		t.Fatal("a non-mapping write must still refuse an unusable mapping left in place")
	}
}
