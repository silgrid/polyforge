package test

import (
	"os"
	"strconv"
	"testing"

	"github.com/silgrid/polyforge/internal/testenv"
)

// TestMain isolates HOME/XDG/POLYFORGE_*/TMPDIR/git config so no test can
// touch the real install or leave temp files behind, hands every fakepi agent this binary's pid (it exits
// once the binary is gone, see fakepiTestPIDEnv), and at exit fails the
// run if any process started from this run's bin dir is still alive.
func TestMain(m *testing.M) {
	os.Setenv(fakepiTestPIDEnv, strconv.Itoa(os.Getpid()))
	testenv.Main(m, binDirAtExit)
}
