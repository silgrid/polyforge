"""pfstep.gomatch: Go's path.Match and filepath.Glob. The expectations are
Go's own answers (go1.26.10, path.Match and filepath.Glob run by a
throwaway program over the same inputs and a tree of the same shape),
hard-coded here: a pattern the Go side reports as ErrBadPattern is None."""

import os
import tempfile
import unittest
from typing import List, Optional, Tuple

from pfstep import gomatch

# (pattern, name, path.Match's answer; None = ErrBadPattern)
MATCH: List[Tuple[str, str, Optional[bool]]] = [
    ('abc', 'abc', True),
    ('*', 'abc', True),
    ('*c', 'abc', True),
    ('a*', 'a', True),
    ('a*', 'abc', True),
    ('a*', 'ab/c', False),
    ('a*/b', 'abc/b', True),
    ('a*/b', 'a/c/b', False),
    ('a*b*c*d*e*/f', 'axbxcxdxe/f', True),
    ('a*b*c*d*e*/f', 'axbxcxdxexxx/f', True),
    ('a*b*c*d*e*/f', 'axbxcxdxe/xxx/f', False),
    ('a*b*c*d*e*/f', 'axbxcxdxexxx/fff', False),
    ('a*b?c*x', 'abxbbxdbxebxczzx', True),
    ('a*b?c*x', 'abxbbxdbxebxczzy', False),
    ('ab[c]', 'abc', True),
    ('ab[b-d]', 'abc', True),
    ('ab[e-g]', 'abc', False),
    ('ab[^c]', 'abc', False),
    ('ab[^b-d]', 'abc', False),
    ('ab[^e-g]', 'abc', True),
    ('a\\*b', 'a*b', True),
    ('a\\*b', 'ab', False),
    ('a?b', 'a☺b', True),
    ('a[^a]b', 'a☺b', True),
    ('a???b', 'a☺b', False),
    ('a[^a][^a][^a]b', 'a☺b', False),
    ('[a-ζ]*', 'α', True),
    ('*[a-ζ]', 'A', False),
    ('a?b', 'a/b', False),
    ('a*b', 'a/b', False),
    ('[\\]a]', ']', True),
    ('[\\-]', '-', True),
    ('[x\\-]', 'x', True),
    ('[x\\-]', '-', True),
    ('[x\\-]', 'z', False),
    ('[\\-x]', 'x', True),
    ('[\\-x]', '-', True),
    ('[\\-x]', 'a', False),
    ('[]a]', ']', None),
    ('[]a]', 'a', None),
    ('[-]', '-', None),
    ('[x-]', 'x', None),
    ('[x-]', '-', None),
    ('[x-]', 'z', None),
    ('[-x]', 'x', None),
    ('[-x]', '-', None),
    ('[-x]', 'a', None),
    ('\\', 'a', None),
    ('a\\', 'a', None),
    ('*\\', 'a', None),
    ('[a-b-c]', 'a', None),
    ('[', 'a', None),
    ('[^', 'a', None),
    ('[^bc', 'a', None),
    ('a[', 'a', None),
    ('a[', 'ab', None),
    ('a[', 'x', None),
    ('a/b[', 'x', None),
    ('*x', 'xxx', True),
    ('a*', '', False),
    ('', '', True),
    ('', 'a', False),
    ('*', '', True),
    ('*', '.hidden', True),
    ('.*', '.hidden', True),
    ('*.go', 'dir/x.go', False),
    ('*/*.go', 'dir/x.go', True),
    ('[!a]', 'b', False),
    ('[!a]', '!', True),
    ('x[^/]y', 'x/y', False),
    ('x[/]y', 'x/y', True),
    ('*a[', 'ba', None),
    ('x*[', 'y', None),
    ('[a-c]x[', 'bx', None),
    ('a*b*', 'a/b', False),
    ('a?', 'a', False),
    ('\\a', 'a', True),
    ('[\\a]', 'a', True),
    ('[a-]x', 'ax', None),
    ('*[', 'x', None),
    ('[]', 'a', None),
    ('[^]', 'a', None),
    ('a*?', 'ab', True),
    ('*.xml', 'r.xml', True),
    ('out/*.xml', 'out/r.xml', True),
    ('[0-9]*', '7z', True),
    ('[^0-9]*', '7z', False),
]

# filepath.Glob(<root>/<pattern>) over the tree GLOB_TREE builds:
# (pattern, ErrBadPattern, the matches with the root as <root>)
GLOB: List[Tuple[str, bool, List[str]]] = [
    ('*', False, ['<root>/.hidden', '<root>/a.go', '<root>/b.go', '<root>/c.txt', '<root>/dangling', '<root>/s3.go', '<root>/sub', '<root>/sub2']),
    ('*.go', False, ['<root>/a.go', '<root>/b.go', '<root>/s3.go']),
    ('*/*.go', False, ['<root>/sub/.y.go', '<root>/sub/x.go', '<root>/sub2/z.go']),
    ('s*/*.go', False, ['<root>/sub/.y.go', '<root>/sub/x.go', '<root>/sub2/z.go']),
    ('a.go', False, ['<root>/a.go']),
    ('nope.go', False, []),
    ('dangling', False, ['<root>/dangling']),
    ('sub', False, ['<root>/sub']),
    ('nomatch*', False, []),
    ('[', True, []),
    ('sub/[', True, []),
    ('*/[', True, []),
    ('./*.go', False, ['<root>/a.go', '<root>/b.go', '<root>/s3.go']),
    ('sub/./*.go', False, ['<root>/sub/.y.go', '<root>/sub/x.go']),
    ('sub//*.go', False, ['<root>/sub/.y.go', '<root>/sub/x.go']),
    ('s*//*.go', False, []),
    ('sub/', False, ['<root>/sub/']),
    ('sub/*', False, ['<root>/sub/.y.go', '<root>/sub/b.txt', '<root>/sub/x.go']),
    ('?.go', False, ['<root>/a.go', '<root>/b.go']),
    ('[ab].go', False, ['<root>/a.go', '<root>/b.go']),
    ('c.txt/*', False, []),
]

GLOB_TREE = (".hidden", "a.go", "b.go", "c.txt", "sub/x.go", "sub/.y.go", "sub/b.txt", "sub2/z.go", "s3.go")


class MatchTest(unittest.TestCase):
    def test_match_is_go_path_match(self) -> None:
        """Mirrors Go's path.Match, its answers derived from Go itself."""
        for pattern, name, want in MATCH:
            with self.subTest(pattern=pattern, name=name):
                if want is None:
                    with self.assertRaises(gomatch.BadPattern):
                        gomatch.match(pattern, name)
                else:
                    self.assertEqual(gomatch.match(pattern, name), want)

    def test_bad_pattern_is_a_value_error_naming_the_syntax_error(self) -> None:
        """Mirrors Go's path.ErrBadPattern ("syntax error in pattern")."""
        with self.assertRaises(ValueError) as cm:
            gomatch.match("[", "a")
        self.assertEqual(str(cm.exception), "syntax error in pattern")


class GlobTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        for rel in GLOB_TREE:
            p = os.path.join(self.root, rel)
            os.makedirs(os.path.dirname(p), exist_ok=True)
            with open(p, "w", encoding="utf-8"):
                pass
        os.symlink(os.path.join(self.root, "gone"), os.path.join(self.root, "dangling"))

    def tearDown(self) -> None:
        self._td.cleanup()

    def test_glob_is_go_filepath_glob(self) -> None:
        """Mirrors Go's filepath.Glob, its answers derived from Go itself:
        dotfiles match ``*``, each directory's names sorted, a pattern
        without a meta character answered by its existence (a dangling
        symlink exists), a malformed pattern an error."""
        for pattern, bad, want in GLOB:
            with self.subTest(pattern):
                if bad:
                    with self.assertRaises(gomatch.BadPattern):
                        gomatch.glob(self.root + "/" + pattern)
                    continue
                got = gomatch.glob(self.root + "/" + pattern)
                self.assertEqual([g.replace(self.root, "<root>", 1) for g in got], want)


if __name__ == "__main__":
    unittest.main()
