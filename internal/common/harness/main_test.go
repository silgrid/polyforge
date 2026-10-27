package harness

import (
	"testing"

	"github.com/silgrid/polyforge/internal/testenv"
)

// TestMain isolates HOME/XDG/POLYFORGE_*/TMPDIR so no test can touch the
// real install or leave temp files behind.
func TestMain(m *testing.M) { testenv.Main(m) }
