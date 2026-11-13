"""pfstep.duration: Go's time.ParseDuration and Duration.String, on the
tables of Go's own time package tests."""

import unittest

from pfstep import duration as d

NS, US, MS, S, M, H = d.NANOSECOND, d.MICROSECOND, d.MILLISECOND, d.SECOND, d.MINUTE, d.HOUR


class FormatTest(unittest.TestCase):
    def test_go_string_table(self) -> None:
        for want, v in [
            ("0s", 0),
            ("1ns", 1 * NS),
            ("1.1µs", 1100 * NS),
            ("2.2ms", 2200 * US),
            ("3.3s", 3300 * MS),
            ("4m5s", 4 * M + 5 * S),
            ("4m5.001s", 4 * M + 5001 * MS),
            ("5h6m7.001s", 5 * H + 6 * M + 7001 * MS),
            ("8m0.000000001s", 8 * M + 1 * NS),
            ("2562047h47m16.854775807s", (1 << 63) - 1),
            ("-2562047h47m16.854775808s", -(1 << 63)),
            ("1h30m0s", 90 * M),
            ("-1.5s", -1500 * MS),
            ("100ms", 100 * MS),
        ]:
            self.assertEqual(d.format(v), want, v)
        with self.assertRaises(ValueError):
            d.format(1 << 63)


class ParseTest(unittest.TestCase):
    def test_go_parse_table(self) -> None:
        for s, want in [
            ("0", 0), ("5s", 5 * S), ("30s", 30 * S), ("1478s", 1478 * S),
            ("-5s", -5 * S), ("+5s", 5 * S), ("-0", 0), ("+0", 0),
            ("5.0s", 5 * S), ("5.6s", 5 * S + 600 * MS), ("5.s", 5 * S), (".5s", 500 * MS),
            ("1.0s", 1 * S), ("1.00s", 1 * S), ("1.004s", 1 * S + 4 * MS), ("1.0040s", 1 * S + 4 * MS),
            ("100.00100s", 100 * S + 1 * MS),
            ("10ns", 10 * NS), ("11us", 11 * US), ("12µs", 12 * US), ("12μs", 12 * US),
            ("13ms", 13 * MS), ("14s", 14 * S), ("15m", 15 * M), ("16h", 16 * H),
            ("3h30m", 3 * H + 30 * M), ("10.5s4m", 4 * M + 10 * S + 500 * MS),
            ("-2m3.4s", -(2 * M + 3 * S + 400 * MS)),
            ("1h2m3s4ms5us6ns", 1 * H + 2 * M + 3 * S + 4 * MS + 5 * US + 6 * NS),
            ("39h9m14.425s", 39 * H + 9 * M + 14 * S + 425 * MS),
            ("52763797000ns", 52763797000 * NS),
            ("0.3333333333333333333h", 20 * M),
            ("9007199254740993ns", (1 << 53) + 1),
            ("9223372036854775807ns", (1 << 63) - 1),
            ("9223372036854775.807us", (1 << 63) - 1),
            ("9223372036s854ms775us807ns", (1 << 63) - 1),
            ("-9223372036854775808ns", -(1 << 63)),
            ("-9223372036854775.808us", -(1 << 63)),
            ("-9223372036s854ms775us808ns", -(1 << 63)),
            ("-2562047h47m16.854775808s", -(1 << 63)),
            ("0.100000000000000000000h", 6 * M),
            ("0.830103483285477580700h", 49 * M + 48 * S + 372539827 * NS),
        ]:
            self.assertEqual(d.parse(s), want, s)

    def test_go_parse_errors(self) -> None:
        for s in [
            "", "3", "-", "s", ".", "-.", ".s", "+.s", "1d", "\x85\x85", "\xffff",
            "hello \xffff world", "9223372036854775808ns", "9223372036854775.808us",
            "9223372036854ms775us808ns", "-9223372036854775809ns", "1_000s", "1 s",
        ]:
            with self.assertRaises(ValueError, msg=repr(s)):
                d.parse(s)

    def test_go_messages(self) -> None:
        for s, msg in [
            ("", 'time: invalid duration ""'),
            ("3", 'time: missing unit in duration "3"'),
            ("1d", 'time: unknown unit "d" in duration "1d"'),
        ]:
            with self.assertRaises(ValueError) as cm:
                d.parse(s)
            self.assertEqual(str(cm.exception), msg)

    def test_round_trip(self) -> None:
        for v in [0, 1, 999, 1000, 1500 * MS, 59 * S, 61 * M, 25 * H + 1, -(3 * H)]:
            self.assertEqual(d.parse(d.format(v)), v)
        self.assertEqual(d.seconds(1500 * MS), 1.5)


if __name__ == "__main__":
    unittest.main()
