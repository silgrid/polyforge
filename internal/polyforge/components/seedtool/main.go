// seedtool is the workstation image's seed-manifest writer and
// verifier (polyforge#162): the build assembles the version directory
// (the release checkout, bin/pf, pi-web, the two harness packages) and
// seedtool writes its manifest — computing the component digests — or
// verifies an assembled seed against its manifest. It is a build tool,
// not a pf subcommand; the install transaction itself lives in the
// components package this tool fronts.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "seedtool:", err)
		os.Exit(1)
	}
}

// run is main's testable body: args[0] names the command.
func run(args []string) error { return errors.New("unimplemented") }
