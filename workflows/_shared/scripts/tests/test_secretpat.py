"""pfstep.secretpat: the Go list's shapes, scan and mask (secretpat_test.go),
and ASCII semantics — the reason every pattern is re.ASCII. The fixtures are
built at run time, so no secret-shaped literal sits in this file."""

import unittest

from pfstep import secretpat


class ScanTest(unittest.TestCase):
    def test_required_shapes(self) -> None:
        for text in [
            "key " + "pf_k1_" + "ABCDEFGHIJKLMNOP",
            "a" * 64,
            "-----BEGIN RSA " + "PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----",
            "sk-" + "abcdefghijklmnopqrstuvwxyz",
            "key " + "key_" + "ABCDEFGHIJKL",
        ]:
            self.assertIsNotNone(secretpat.scan(text), text)
        for clean in ["w11 clean", "short deadbeef", "-----BEGIN PUBLIC KEY-----", "please review the plan",
                      "monkey_business123 is ordinary prose"]:
            self.assertIsNone(secretpat.scan(clean), clean)

    def test_first_shape_in_scan_order(self) -> None:
        # Go's order decides, not the position: the openai shape is listed first
        self.assertEqual(secretpat.scan("sk-ant-" + "A" * 30), "anthropic_key")
        self.assertEqual(secretpat.scan("sk-ant-" + "A" * 30 + " sk-" + "b" * 25), "openai_key")
        self.assertEqual([n for n, _ in secretpat.PATTERNS][-1], "hex64")

    def test_ascii_semantics(self) -> None:
        # Go's \b is ASCII: "é" is no word character, so a boundary stands
        # before "key_" (a Unicode \b would see none)
        self.assertEqual(secretpat.scan("é" + "key_" + "ABCDEFGHIJKL"), "polyforge_short_key")
        # Go's [0-9a-fA-F] and \b: a run glued to a Unicode letter is still
        # bounded
        self.assertEqual(secretpat.scan("ü" + "f" * 64 + "ü"), "hex64")
        # Go's \s has no vertical tab: the flag's value is not taken across one
        self.assertEqual(secretpat.mask("--password\x0bhunter"), "--password\x0bhunter")


class MaskTest(unittest.TestCase):
    def test_replaces_credential_shapes(self) -> None:
        gh = "ghp_" + "Zz9" * 12
        text = ("push " + gh + " via https://deploy:pa55-word@proxy.example:3128/x and git://tok@h/r\n"
                "DEPLOY_API_KEY=k-123 GITHUB_TOKEN=t-456 db_password=p-789 --password  hunter-9 ok")
        got = secretpat.mask(text)
        for secret in [gh, "deploy:pa55-word", "tok@", "k-123", "t-456", "p-789", "hunter-9"]:
            self.assertNotIn(secret, got)
        for kept in ["push [masked github_token]", "https://***@proxy.example:3128/x", "git://***@h/r",
                     "DEPLOY_API_KEY=***", "--password  *** ok"]:
            self.assertIn(kept, got)
        clean = "ok 12 tests passed in 3.2s (see https://example.com/run/7)"
        self.assertEqual(secretpat.mask(clean), clean)


if __name__ == "__main__":
    unittest.main()
