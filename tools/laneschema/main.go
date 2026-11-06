// Command laneschema writes the lane schema of this build's workflow
// contract, rendered from the contract registry (workflow.LaneSchema), to
// <workflows>/_schema/workflow-<N>.schema.json — run from the repository
// root as `go run ./tools/laneschema` (or `go generate
// ./internal/common/workflow`). The workflow package's tests regenerate
// it and fail on any difference from the committed file.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

func main() {
	dir := flag.String("workflows", "workflows", "the lanes root the schema directory sits in")
	flag.Parse()
	raw, err := workflow.LaneSchema()
	if err != nil {
		fmt.Fprintln(os.Stderr, "laneschema:", err)
		os.Exit(1)
	}
	out := filepath.Join(*dir, workflow.SchemaDir, workflow.SchemaFile(workflow.ContractVersion))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "laneschema:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "laneschema:", err)
		os.Exit(1)
	}
	fmt.Println(out)
}
