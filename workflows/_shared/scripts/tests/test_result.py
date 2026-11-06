"""pfstep.result: the verdict follows the exit code only, the document keeps
the schema's key order, a written result satisfies the published schema,
and read_result refuses a document that is not one."""

import datetime
import json
import os
import tempfile
import unittest
from typing import Any, Callable, Dict

from pfstep import result

from . import schemacheck


def _schema() -> Dict[str, Any]:
    with open(result.SCHEMA_PATH, encoding="utf-8") as f:
        doc: Dict[str, Any] = json.load(f)
    return doc


class VerdictTest(unittest.TestCase):
    def test_verdict_follows_the_exit_code_only(self) -> None:
        # the table the Go step commands' VerdictOf answered
        for code, want in {0: "pass", 1: "fail", 2: "error", 3: "partial", 4: "error", 124: "error", -1: "error"}.items():
            self.assertEqual(result.verdict_of(code), want, code)
            self.assertEqual(result.Result(command="x", version="v", exit_code=code).verdict, want)


class DocumentTest(unittest.TestCase):
    def test_key_order_is_the_schemas(self) -> None:
        doc = result.Result(command="x.py", version="v1").to_dict()
        self.assertEqual(tuple(doc), result.FIELDS)
        self.assertEqual(tuple(_schema()["required"]), result.FIELDS)
        self.assertEqual(tuple(_schema()["properties"]), result.FIELDS)

    def test_written_result_satisfies_the_schema(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "artifacts", "x", "result.json")
            res = result.begin("x.py", ["--json", "--out", out], "v1", {"PF_ROUND": "3"})
            res.heads = {"app": "0" * 40}
            res.details = {"checks": [{"name": "a", "status": "pass"}]}
            res.logs = ["logs/x.log"]
            res.summary = "one line"
            result.finish(res, 1, out)
            with open(out, encoding="utf-8") as f:
                raw = f.read()
            doc = json.loads(raw)
            self.assertEqual(schemacheck.errors(_schema(), doc), [])
            self.assertEqual(doc["round"], "3")
            self.assertEqual(doc["verdict"], "fail")
            self.assertTrue(raw.endswith("}\n"))
            self.assertEqual(result.read_result(out)["exit_code"], 1)

    def test_schema_refuses_a_broken_document(self) -> None:
        # negative control of the checker itself: each mutation is caught
        good = result.Result(command="x.py", version="v1", started_at="2026-11-12T01:02:03Z",
                             ended_at="2026-11-12T01:02:04Z", exit_code=0).to_dict()
        self.assertEqual(schemacheck.errors(_schema(), good), [])
        mutations: Dict[str, Callable[[Dict[str, Any]], Any]] = {
            "a required field dropped": lambda d: d.pop("verdict"),
            "the verdict not the exit code's": lambda d: d.update(verdict="fail"),
            "exit 7 not an error": lambda d: d.update(exit_code=7),
            "an unknown key": lambda d: d.update(extra=1),
            "a sub-second stamp": lambda d: d.update(started_at="2026-11-12T01:02:03.5Z"),
            "a non-string head": lambda d: d.update(heads={"app": 1}),
            "argv not strings": lambda d: d.update(argv=[1]),
            "another schema tag": lambda d: d.update(schema="pf-step-result/2"),
            "details not an object": lambda d: d.update(details=[]),
            "exit code a string": lambda d: d.update(exit_code="0"),
        }
        for why, mutate in mutations.items():
            doc = json.loads(json.dumps(good))
            mutate(doc)
            self.assertNotEqual(schemacheck.errors(_schema(), doc), [], why)

    def test_stamp_is_utc_whole_seconds(self) -> None:
        t = datetime.datetime(2026, 11, 12, 9, 8, 7, 654321, tzinfo=datetime.timezone(datetime.timedelta(hours=9)))
        self.assertEqual(result.stamp(t), "2026-11-12T00:08:07Z")
        with self.assertRaises(ValueError):
            result.stamp(datetime.datetime(2026, 1, 1))


class ReadResultTest(unittest.TestCase):
    def _write(self, d: str, doc: Any) -> str:
        p = os.path.join(d, "r.json")
        with open(p, "w", encoding="utf-8") as f:
            json.dump(doc, f)
        return p

    def test_refuses_what_is_not_a_result(self) -> None:
        good = result.Result(command="x.py", version="v1", exit_code=0).to_dict()
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(result.read_result(self._write(d, good))["verdict"], "pass")
            for why, doc in {
                "not an object": [good],
                "another schema": dict(good, schema="other/1"),
                "missing heads": {k: v for k, v in good.items() if k != "heads"},
                "a verdict the exit code does not decide": dict(good, verdict="fail"),
                "a boolean exit code": dict(good, exit_code=True),
            }.items():
                with self.assertRaises(result.ResultError, msg=why):
                    result.read_result(self._write(d, doc))
            with self.assertRaises(result.ResultError):
                result.read_result(os.path.join(d, "absent.json"))


if __name__ == "__main__":
    unittest.main()
