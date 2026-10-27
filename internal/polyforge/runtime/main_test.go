package runtime

import (
	"testing"

	"github.com/silgrid/polyforge/internal/testenv"
)

// TestMain isolates HOME/XDG/POLYFORGE_* so no test can touch the real
// install, and reaps every process group the helpers started.
func TestMain(m *testing.M) { testenv.Main(m, reapTestGroups) }
