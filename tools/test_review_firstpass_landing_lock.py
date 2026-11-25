"""Unit tests for tools/review-firstpass's landing-lock metrics (polyforge#249).

The tool has no .py suffix; it is loaded by path. Expected values are
hand-derived from the ticket's fixtures, never read out of the tool:

  cycle A: queued t=0, granted t=12 s (queued_ms 12000)
  cycle B: queued t=20, granted t=28 (queued_ms 8000)
  duplicate queued/granted, one renew_failed + lost, one duplicate lost
  => wait 20000 ms, lost cycles 1
  two actual rewinds whose reason carries `branch moved since verification`
  => moved-base 2; a verification FAIL, an owner_rewind and an ordinary
  conflict add nothing.

Run from the repository root:
  python3 -m unittest discover -s tools -p 'test_review_firstpass*.py'
"""

import importlib.machinery
import importlib.util
import io
import json
import os
import sys
import tempfile
import unittest
import urllib.parse

TOOL = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'review-firstpass')
_loader = importlib.machinery.SourceFileLoader('review_firstpass_landing', TOOL)
_spec = importlib.util.spec_from_loader(_loader.name, _loader)
rf = importlib.util.module_from_spec(_spec)
_loader.exec_module(rf)


def ev(seq, kind, at, body):
    return {'seq': seq, 'at': rf.iso_ts(at), 'kind': kind, 'body': body}


def key(repo='github.com/o/app', branch='epic/245'):
    return {'repo': repo, 'branch': branch}


def queued(seq, at, cycle, repo='github.com/o/app', branch='epic/245', pos=1):
    return ev(seq, 'merge_lock_queued', at, {'cycle_id': cycle, 'reason': 'waiting',
                                             'position': pos, 'keys': [{**key(repo, branch), 'position': pos}]})


def granted(seq, at, cycle, queued_ms, repo='github.com/o/app', branch='epic/245'):
    return ev(seq, 'merge_lock_granted', at, {'cycle_id': cycle, 'reason': 'granted',
                                              'keys': [{**key(repo, branch), 'position': 1, 'queued_ms': queued_ms}]})


def closed_fixture():
    out = [
        queued(1, '2026-11-25T00:00:00Z', 'c-a'),
        granted(2, '2026-11-25T00:00:12Z', 'c-a', 12000),
        # consumer-side defense: the same cycle/key queued twice is one wait
        queued(3, '2026-11-25T00:00:01Z', 'c-a'),
        granted(4, '2026-11-25T00:00:12Z', 'c-a', 12000),
        queued(5, '2026-11-25T00:00:20Z', 'c-b'),
        granted(6, '2026-11-25T00:00:28Z', 'c-b', 8000),
        ev(7, 'merge_lock_renew_failed', '2026-11-25T00:00:30Z', {'cycle_id': 'c-b', 'reason': 'renew_failed'}),
        ev(8, 'merge_lock_lost', '2026-11-25T00:00:30Z', {'cycle_id': 'c-b', 'reason': 'lost'}),
        ev(9, 'merge_lock_lost', '2026-11-25T00:00:31Z', {'cycle_id': 'c-b', 'reason': 'lost'}),
        ev(10, 'rewind', '2026-11-25T00:01:00Z', {'gate': 'merge', 'target': 'sync',
                                                  'reason': 'the default branch moved since verification (a: 1, now: 2)'}),
        ev(11, 'rewind', '2026-11-25T00:02:00Z', {'gate': 'merge', 'target': 'sync',
                                                  'reason': 'the target branch moved since verification (epic/245: a, now b)'}),
        ev(12, 'rewind', '2026-11-25T00:03:00Z', {'gate': 'verification', 'target': 'build',
                                                  'reason': 'verification FAIL: two findings'}),
        ev(13, 'owner_rewind', '2026-11-25T00:04:00Z', {'reason': 'branch moved since verification'}),
        ev(14, 'rewind', '2026-11-25T00:05:00Z', {'gate': 'sync', 'target': 'build',
                                                  'reason': 'an ordinary merge conflict'}),
    ]
    return out


class LandingLockStatsTest(unittest.TestCase):
    def test_closed_cycles_lost_and_moved_base(self):
        st = rf.landing_lock_stats(closed_fixture())
        self.assertEqual(st['state'], 'complete')
        self.assertEqual(st['wait_ms'], 20000)
        self.assertEqual(st['lost'], 1, 'renew_failed and a duplicate lost count one cycle')
        self.assertEqual(st['moved_base'], 2, 'only the two moved-base reasons count')
        self.assertEqual(st['open_intervals'], 0)

    def test_open_missing_and_old_logs(self):
        only_queued = [queued(1, '2026-11-25T00:00:00Z', 'c-a')]
        st = rf.landing_lock_stats(only_queued)
        self.assertIsNone(st['wait_ms'])
        self.assertEqual(st['state'], 'incomplete')
        self.assertEqual(st['open_intervals'], 1)
        granted_only = [granted(1, '2026-11-25T00:00:12Z', 'c-a', 12000)]
        self.assertIsNone(rf.landing_lock_stats(granted_only)['wait_ms'])
        self.assertIsNone(rf.landing_lock_stats([])['wait_ms'], 'no events is unknown, never 0')
        self.assertIsNone(rf.landing_lock_stats(None)['lost'], 'an absent event file is unknown')
        # an old log with no rewind reason cannot report moved-base as 0
        old = [ev(1, 'rewind', '2026-11-25T00:01:00Z', {'gate': 'merge', 'target': 'sync'})]
        self.assertIsNone(rf.landing_lock_stats(old)['moved_base'])

    def test_multi_repo_union_not_sum(self):
        events = [
            queued(1, '2026-11-25T00:00:00Z', 'c-a', repo='github.com/o/a'),
            granted(2, '2026-11-25T00:00:10Z', 'c-a', 10000, repo='github.com/o/a'),
            queued(3, '2026-11-25T00:00:05Z', 'c-a', repo='github.com/o/b'),
            granted(4, '2026-11-25T00:00:15Z', 'c-a', 10000, repo='github.com/o/b'),
        ]
        st = rf.landing_lock_stats(events)
        self.assertEqual(st['wait_ms'], 15000, 'overlapping keys union to the ticket wall wait')
        self.assertEqual(sorted(st['key_wait_ms']), [10000, 10000], 'key subtotals stay separate')

    def test_cli_fixture_prints_the_same_numbers(self):
        with tempfile.TemporaryDirectory() as tmp:
            fixture = export_fixture(tmp)
            out, saved = io.StringIO(), sys.stdout
            try:
                sys.stdout = out
                rc = rf.main([fixture['capsules'], '--events', fixture['events'], '--csv', fixture['csv']])
            finally:
                sys.stdout = saved
            self.assertEqual(rc, 0)
            report = out.getvalue()
            self.assertIn('== landing lock (polyforge#249) ==', report)
            self.assertIn('wait 20000 ms', report)
            self.assertIn('lost 1', report)
            self.assertIn('moved-base 2', report)
            with open(fixture['csv']) as f:
                header = f.readline()
            self.assertIn('landing_wait_ms', header)
            self.assertIn('landing_lost_cycles', header)
            self.assertIn('landing_moved_base', header)


def export_fixture(root):
    """The AC2 fixture: one capsule, its event file and a CSV target (a test
    helper, not a product CLI)."""
    wi = 'wi_landing_fixture'
    caps = os.path.join(root, 'capsules')
    events = os.path.join(root, 'events')
    os.makedirs(os.path.join(caps, 'fixture-capsule'), exist_ok=True)
    os.makedirs(events, exist_ok=True)
    with open(os.path.join(caps, 'fixture-capsule', 'capsule.json'), 'w') as f:
        json.dump({'work_item_id': wi, 'bundle_name': 'epic', 'created_at': '2026-11-25T00:00:00Z'}, f)
    path = os.path.join(events, 'wi-' + urllib.parse.quote(wi, safe='') + '.jsonl')
    with open(path, 'w') as f:
        for e in closed_fixture():
            body = {'at': e['at'].strftime('%Y-%m-%dT%H:%M:%SZ'), 'kind': e['kind'],
                    'work_item': wi, 'body': e['body'], 'seq': e['seq']}
            f.write(json.dumps(body) + '\n')
    return {'capsules': caps, 'events': events, 'csv': os.path.join(root, 'out.csv')}


if __name__ == '__main__':
    if len(sys.argv) == 3 and sys.argv[1] == '--export-fixture':
        fixture = export_fixture(sys.argv[2])
        print(json.dumps(fixture))
    else:
        unittest.main()
