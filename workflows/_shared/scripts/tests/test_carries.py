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


class HeaderParseTest(unittest.TestCase):
    """parse_header: the three-way classification the scope engine's own
    header reader reports (declared / undeclared / malformed). The expected
    values are derived from the shell engine's cr_header contract (the
    opening comment block, a carries: line's comma- or blank-separated
    globs, an empty value or a carries word without its colon being
    malformed) — independent of this module's implementation."""

    def test_declared_unions_every_line(self) -> None:
        src = ("#!/usr/bin/env node\n"            # `#!` is an ordinary comment lead
               "// carries: lib/a*, components/A*\n"
               "\n"                                 # a blank line does not end the block
               "#  carries: app/x/*  hooks/y*\n"    # `#` comments carry too
               "// carries:lib/no-space\n"          # no blank after the colon
               "import x from 'y';\n"
               "// carries: never/*\n")             # after the first code line: not a declaration
        self.assertEqual(carries.parse_header(src),
                         ("declared", ["lib/a*", "components/A*", "app/x/*", "hooks/y*", "lib/no-space"]))

    def test_undeclared(self) -> None:
        self.assertEqual(carries.parse_header(""), ("undeclared", []))
        self.assertEqual(carries.parse_header("// a plain comment\n# another\ncode();\n"), ("undeclared", []))
        # a shebang is a comment line; the block may be empty of declarations
        self.assertEqual(carries.parse_header("#!/bin/sh\nexit 0\n"), ("undeclared", []))

    def test_malformed_empty_value(self) -> None:
        self.assertEqual(carries.parse_header("// carries: \ncode();\n"), ("malformed", []))
        self.assertEqual(carries.parse_header("# carries:\ncode();\n"), ("malformed", []))

    def test_malformed_word_without_colon(self) -> None:
        # `carries` followed by a non-identifier character, without its colon
        self.assertEqual(carries.parse_header("# carries lib/**\ncode();\n"), ("malformed", []))
        # `carried:` is not the carries word
        self.assertEqual(carries.parse_header("// carried: lib/**\ncode();\n"), ("undeclared", []))

    def test_malformed_wins_over_earlier_good_lines(self) -> None:
        src = "// carries: lib/a*\n// carries:\ncode();\n"
        self.assertEqual(carries.parse_header(src), ("malformed", []))

    def test_parse_carries_agrees_with_parse_header(self) -> None:
        for src in ("", "// carries: a*\n", "// carries:\n", "# carries x\n", "a\n// carries: b\n"):
            status, globs = carries.parse_header(src)
            self.assertEqual(carries.parse_carries(src), globs if status == "declared" else [], src)


if __name__ == "__main__":
    unittest.main()
