"""pfstep.deadline: the same answers as Go's verifyrun.DefaultDeadlineFor —
its eight cases (plan_test.go TestDefaultDeadlineFor) — and Go's integer
parsing (strconv.Atoi after TrimSpace)."""

import unittest

from pfstep import deadline, duration

MIN = duration.MINUTE
SEC = duration.SECOND


class DeadlineTest(unittest.TestCase):
    def test_the_go_cases(self) -> None:
        cases = {
            "": deadline.DEFAULT_DEADLINE,
            "BASH_MAX_TIMEOUT_MS=1740000": 27 * MIN,
            "BASH_MAX_TIMEOUT_MS=60000": 30 * SEC,  # half the bound, not the whole minute
            "BASH_MAX_TIMEOUT_MS=bogus": deadline.DEFAULT_DEADLINE,
            "PF_TOOL_WINDOW_MS=1800000": 28 * MIN,
            "PF_TOOL_WINDOW_MS=600000": 8 * MIN,
            "PF_TOOL_WINDOW_MS=bogus": deadline.DEFAULT_DEADLINE,
        }
        for kv, want in cases.items():
            env = {"PATH": "/bin"}
            if kv:
                k, v = kv.split("=", 1)
                env[k] = v
            self.assertEqual(deadline.default_deadline_for(env), want, kv)
        # the eighth: the tighter of the two bounds wins
        both = {"PATH": "/bin", "BASH_MAX_TIMEOUT_MS": "1740000", "PF_TOOL_WINDOW_MS": "3600000"}
        self.assertEqual(deadline.default_deadline_for(both), 27 * MIN)
        self.assertEqual(deadline.DEFAULT_DEADLINE, 25 * MIN)

    def test_go_integer_parsing(self) -> None:
        for raw, want in {
            " 600000 ": 8 * MIN,  # TrimSpace
            "+600000": 8 * MIN,  # Atoi takes a sign
            "-600000": deadline.DEFAULT_DEADLINE,  # <= 0 is no bound
            "0": deadline.DEFAULT_DEADLINE,
            "600_000": deadline.DEFAULT_DEADLINE,  # Python's int() would take it; Atoi does not
            "６００000": deadline.DEFAULT_DEADLINE,  # nor other digits
            "6e5": deadline.DEFAULT_DEADLINE,
            "1": 500 * duration.MICROSECOND,  # half of a 1 ms bound
        }.items():
            self.assertEqual(deadline.default_deadline_for({"PF_TOOL_WINDOW_MS": raw}), want, repr(raw))


if __name__ == "__main__":
    unittest.main()
