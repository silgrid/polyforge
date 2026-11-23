# carries: workflows/_shared/scripts/pfstep/engine.py, workflows/_shared/scripts/tests/test_engine.py
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


class DecodeReportGroupsTest(unittest.TestCase):
    """polyforge#244: the full gate's declared groups (polyforge#244). A
    command bound to a group carries ``group``; the report's
    ``full_parallel`` is the parallelism, 1 (serial) when it names none."""

    def report(self, *extra: str) -> str:
        cmds = ('{"name":"fast-1","phase":"fast","cmd":"echo f","group":"nope"},'
                '{"name":"full-1","phase":"full","cmd":"echo one","group":"go"},'
                '{"name":"full-2","phase":"full","cmd":"echo two"}')
        body = '{"schema":1,"stack":"go","mode":"full","commands":[' + cmds + ']'
        if extra:
            body += "," + ",".join(extra)
        return body + "}"

    def test_a_bound_command_carries_its_group(self) -> None:
        rep = engine.decode_report(self.report().encode())
        self.assertEqual([c.group for c in rep.commands], ["nope", "go", ""])
        self.assertEqual(rep.parallel, 1, "a report that names no parallelism runs serially")

    def test_the_reports_parallelism_is_decoded(self) -> None:
        rep = engine.decode_report(self.report('"full_parallel":2').encode())
        self.assertEqual(rep.parallel, 2)
        self.assertEqual(rep.commands[1].group, "go")

    def test_a_non_positive_parallelism_is_serial(self) -> None:
        for bad in ('"full_parallel":0', '"full_parallel":-3'):
            with self.subTest(bad):
                self.assertEqual(engine.decode_report(self.report(bad).encode()).parallel, 1)


class ArgvOfTest(unittest.TestCase):
    """How the engine runs (polyforge#50): itself when it is executable,
    else under `python3` — resolved on the PATH of whatever runs the argv
    (a lane snapshot's files are mode 0444; the argv reaches a verification
    container as `docker exec`, where the image's PATH must hold python3,
    never the step host's own interpreter path)."""

    def test_executable_engine_runs_itself(self) -> None:
        import os
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            e = os.path.join(d, "verify-scope")
            with open(e, "w") as f:
                f.write("#!/usr/bin/env python3\n")
            os.chmod(e, 0o755)
            self.assertEqual(engine.argv_of(e), [e])

    def test_read_only_engine_runs_under_python3(self) -> None:
        import os
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            e = os.path.join(d, "verify-scope")
            with open(e, "w") as f:
                f.write("#!/usr/bin/env python3\n")
            os.chmod(e, 0o444)
            self.assertEqual(engine.argv_of(e), ["python3", e])


if __name__ == "__main__":
    unittest.main()
