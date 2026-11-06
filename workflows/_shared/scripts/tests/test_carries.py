"""pfstep.carries: the ``carries:`` globs a test file's opening comment
block declares (the Go table of verifyrun/w403_light_tier_test.go,
asserting the Go behavior)."""

import unittest

from pfstep import carries


class ParseCarriesTest(unittest.TestCase):
    def test_read_carries(self) -> None:
        """Port of TestW403ReadCarries (w403_light_tier_test.go)."""
        src = ("#!/usr/bin/env node\n// carries: lib/a*, components/A*\n\n// carries: app/x/*  hooks/y*\n"
               "import x from 'y';\n// carries: never/*\n")
        self.assertEqual(carries.parse_carries(src), ["lib/a*", "components/A*", "app/x/*", "hooks/y*"])


if __name__ == "__main__":
    unittest.main()
