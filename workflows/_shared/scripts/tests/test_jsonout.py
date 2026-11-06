"""pfstep.jsonout: the order the value was built, UTF-8 kept, NaN refused."""

import json
import unittest

from pfstep import jsonout


class JsonOutTest(unittest.TestCase):
    def test_insertion_order_never_sorted(self) -> None:
        v = {"zeta": 1, "alpha": {"y": [], "b": {}}, "mid": "é—中"}
        out = jsonout.dumps(v)
        self.assertEqual(out, '{\n  "zeta": 1,\n  "alpha": {\n    "y": [],\n    "b": {}\n  },\n  "mid": "é—中"\n}\n')
        self.assertEqual(json.loads(out), v)
        self.assertEqual(jsonout.line(v), '{"zeta":1,"alpha":{"y":[],"b":{}},"mid":"é—中"}')

    def test_nan_is_refused(self) -> None:
        for bad in (float("nan"), float("inf")):
            with self.assertRaises(ValueError):
                jsonout.dumps({"x": bad})
            with self.assertRaises(ValueError):
                jsonout.line([bad])


if __name__ == "__main__":
    unittest.main()
