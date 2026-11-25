"""C1 prefers the server's granted `since` over a local queued stamp
(polyforge#249 Q15; review r1#4). Loaded by path, like the other C1 tests."""

import importlib.machinery
import importlib.util
import os
import unittest

TOOL = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'review-firstpass')
_loader = importlib.machinery.SourceFileLoader('review_firstpass_server_time', TOOL)
_spec = importlib.util.spec_from_loader(_loader.name, _loader)
rf = importlib.util.module_from_spec(_spec)
_loader.exec_module(rf)


def ev(seq, kind, at, body):
    return {'seq': seq, 'at': rf.iso_ts(at), 'kind': kind, 'body': body}


class ServerSinceTest(unittest.TestCase):
    def test_granted_server_since_wins_over_a_local_queued_stamp(self):
        key = {'repo': 'github.com/o/app', 'branch': 'epic/245'}
        events = [
            ev(1, 'merge_lock_queued', '2026-11-25T00:00:05Z',
               {'cycle_id': 'c-a', 'reason': 'waiting', 'keys': [{**key, 'position': 1}]}),
            ev(2, 'merge_lock_granted', '2026-11-25T00:00:12Z',
               {'cycle_id': 'c-a', 'reason': 'granted', 'keys': [{**key, 'position': 1,
                                                                 'since': '2026-11-25T00:00:00Z', 'queued_ms': 12000}]}),
        ]
        st = rf.landing_lock_stats(events)
        self.assertEqual(st['wait_ms'], 12000, 'the granted key carries the server start')
        self.assertEqual(st['open_intervals'], 0)


if __name__ == '__main__':
    unittest.main()
