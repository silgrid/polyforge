package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// TestConflictingNamedCellsAreAllInvalidInEitherOrder: two named Cells on
// one AIHub are both invalid whatever their file order; an unrelated Cell
// loads; the default Cell is the baseline.
func TestConflictingNamedCellsAreAllInvalidInEitherOrder(t *testing.T) {
	for _, names := range [][2]string{{"alpha", "beta"}, {"zeta", "eta"}} {
		dir := t.TempDir()
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		write("cli.toml", "server = \"http://hub-d:1\"\napi_key = \"k\"\n")
		write("cli-"+names[0]+".toml", "server = \"http://shared:2\"\napi_key = \"k1\"\n")
		write("cli-"+names[1]+".toml", "server = \"http://SHARED:2/\"\napi_key = \"k2\"\n")
		write("cli-free.toml", "server = \"http://free:3\"\napi_key = \"k3\"\n")
		set, err := cli.LoadHostSet(filepath.Join(dir, "cli.toml"))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, c := range set.Cells {
			got[c.Profile] = c.Invalid
		}
		for _, n := range names {
			if !strings.Contains(got[n], "same AIHub server") {
				t.Fatalf("%v: Cell %s must be invalid naming the conflict: %q", names, n, got[n])
			}
		}
		if got["free"] != "" {
			t.Fatalf("an unrelated Cell was invalidated: %q", got["free"])
		}
	}
}
