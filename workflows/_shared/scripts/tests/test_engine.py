"""pfstep.engine: the scope engine's report checked against its contract
(schema 1, a mode, named commands with a known phase) — the Go table of
verifyrun/rules_test.go, asserting the Go behavior."""

import unittest

from pfstep import engine


class DecodeReportTest(unittest.TestCase):
    def test_decode_report_contract(self) -> None:
        """Port of TestDecodeReportContract (rules_test.go)."""
        good = ('{"schema":1,"stack":"go","analyzer":"go","conf":"absent","conf_changed_in_diff":false,"mode":"full","paths":[],'
                '"commands":[{"name":"fast-1","phase":"fast","cmd":"go vet ./...","requires_env":[],"requires_cmd":[],'
                '"timeout":"20m","source":"default"}],'
                '"test_glob":[],"base_setup":[],"runner":null,"diagnostics":[]}')
        rep = engine.decode_report(good.encode())
        self.assertEqual(rep.commands[0].timeout, "20m")
        for bad in (
            '{"schema":2,"mode":"full","commands":[]}',
            '{"schema":1,"mode":"partial","commands":[]}',
            '{"schema":1,"mode":"full","commands":[{"name":"x","phase":"slow","cmd":"true"}]}',
            '{"schema":1,"mode":"full","commands":[{"name":"","phase":"fast","cmd":"true"}]}',
            "mode: full",
        ):
            with self.subTest(bad):
                with self.assertRaises(engine.ReportError):
                    engine.decode_report(bad.encode())


if __name__ == "__main__":
    unittest.main()
