"""Unit tests for tools/review-firstpass (polyforge#242).

The tool has no .py suffix; it is loaded by path. The cases are the ticket's
fixture table (polyforge#242, 分析 2): the cross-round reference chain, the
owner's asks.jsonl rulings (entry numbers only, never inferred from prose),
the class/model summary, and the unknown-outcome reasons. Expected values are
hand-derived from the ticket, never read out of the tool.

Run from the repository root:
  python3 -m unittest discover -s tools -p 'test_review_firstpass*.py'
"""

import importlib.machinery
import importlib.util
import json
import os
import tempfile
import unittest

TOOL = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'review-firstpass')
_loader = importlib.machinery.SourceFileLoader('review_firstpass', TOOL)
_spec = importlib.util.spec_from_loader(_loader.name, _loader)
rf = importlib.util.module_from_spec(_spec)
_loader.exec_module(rf)


def _review(round_no, verdict, findings):
    return f'# review · 第 {round_no} 轮\n\n**结论：{verdict}**\n\n{findings}\n'


def _versions(*texts):
    return [{'round': rf.review_round(t) or i + 1,
             'verdict': rf.review_verdict(t), 'text': t}
            for i, t in enumerate(texts)]


def _item(text, round_no):
    items = rf.parse_blocking_items(text, round_no)
    return items


class BlockingParseTest(unittest.TestCase):
    def test_old_formats_and_non_blocking(self):
        text = _review(1, 'FAIL', (
            '## 发现\n\n'
            '### #1 · blocking · `guarantee` — a\n\n'
            '### #2 blocking — `bug` — b\n\n'
            '4. **ac_gap AC2**：c\n\n'
            '- **#4 · non-blocking · 流程偏离**：not counted\n'))
        items = _item(text, 1)
        self.assertEqual([(i['item'], i['class']) for i in items],
                         [(1, 'guarantee'), (2, 'bug'), (4, 'ac_gap')])

    def test_numbered_body_list_under_a_blocking_item_is_not_new_items(self):
        text = _review(1, 'FAIL', (
            '## 阻断项\n\n'
            '### #1 test_weakened — the first blocker\n\n'
            '1. first repair step\n'
            '2. second repair step\n\n'
            '### #2 bug — the second blocker\n'))
        items = _item(text, 1)
        self.assertEqual([(i['item'], i['class']) for i in items],
                         [(1, 'test_weakened'), (2, 'bug')])

    def test_an_issue_number_in_prose_is_not_an_item(self):
        text = _review(1, 'FAIL', (
            '## 阻断项\n\n'
            '**#1 blocking · bug — the only blocker**\n\n'
            '- a note that cites (polyforge#117) in prose\n'))
        items = _item(text, 1)
        self.assertEqual([(i['item'], i['class']) for i in items], [(1, 'bug')])

    def test_mention_of_an_earlier_round_is_not_an_item(self):
        text = _review(2, 'PASS', '## 上一轮发现逐条核对\n\n- **r1#1 已修。**\n')
        self.assertEqual(_item(text, 2), [])

    def test_unparsable_fail_version_reports_its_count(self):
        text = '# review · 第 1 轮\n\n**结论：FAIL** —— 1 条阻断。\n\n## 发现\n\n- 这个版本的条目写法解析不了\n'
        self.assertEqual(_item(text, 1), [])
        self.assertEqual(rf.blocking_count_in_summary(text), 1)


class BlockingOutcomesTest(unittest.TestCase):
    def setUp(self):
        self.cap = tempfile.mkdtemp()

    def _asks(self, *entries):
        os.makedirs(os.path.join(self.cap, 'runtime-state'), exist_ok=True)
        with open(os.path.join(self.cap, 'runtime-state', 'asks.jsonl'), 'w') as f:
            for e in entries:
                f.write(json.dumps(e) + '\n')

    def _one_item(self):
        return _versions(_review(1, 'FAIL', '## 发现\n\n### #1 blocking · bug — x\n'))

    def test_owner_reply_without_an_item_number_is_unknown(self):
        self._asks({'at': '2026-11-14T11:00:00Z', 'kind': 'reply', 'text': '按推荐'})
        [chain] = rf.chain_findings(self.cap, self._one_item())
        self.assertEqual(chain['outcome'], 'unknown')
        self.assertEqual(chain['reason'], 'owner words without an item number')

    def test_multiple_owner_candidates_are_unknown(self):
        self._asks(
            {'at': '2026-11-14T11:00:00Z', 'kind': 'reply', 'text': 'r1#2 改判非阻断'},
            {'at': '2026-11-14T11:01:00Z', 'kind': 'message', 'text': 'r1#3 改判非阻断'})
        [chain] = rf.chain_findings(self.cap, self._one_item())
        self.assertEqual(chain['outcome'], 'unknown')
        self.assertEqual(chain['reason'], 'multiple candidates')

    def test_same_text_on_two_items_is_two_chains(self):
        text = _review(1, 'FAIL', (
            '## 发现\n\n'
            '### #1 blocking · bug — the same words\n\n'
            '### #2 blocking · bug — the same words\n'))
        chains = rf.chain_findings(self.cap, _versions(text))
        self.assertEqual(len(chains), 2)
        self.assertEqual({c['item'] for c in chains}, {1, 2})

    def test_cross_round_chain_ends_fixed(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · guarantee — x\n'),
            _review(2, 'FAIL', '## 发现\n\n### #1 blocking · guarantee（残留 r1#1）— x\n'),
            _review(3, 'PASS', '## 上一轮发现逐条核对\n\n- **r2#1 已修。**\n'))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')
        self.assertEqual(chain['rounds'], [1, 2])

    def test_fixed_wins_over_a_residue_reference(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · bug — x\n'),
            _review(2, 'PASS', '## 上一轮发现逐条核对\n\n- **r1#1 已修（残留 r0#9 的旧描述）**\n'))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')

    def test_files_still_there_does_not_reopen_a_fix(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · bug — x\n'),
            _review(2, 'PASS', '## 上一轮发现逐条核对\n\n- **r1#1 已修。** 两个文件都还在\n'))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')

    def test_a_later_mention_does_not_override_the_disposition(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #4 blocking · test_weakened — x\n'),
            _review(2, 'PASS', (
                '## 上一轮发现逐条核对\n\n'
                '- **r1#4（test_weakened u9）已修。** build 没有采用 r1 给的方案。\n\n'
                '- **N17**（新）r1#4 的处理方式是撤销单元记录……所以不构成 ac_gap。\n')))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')

    def test_explicit_fix_outranks_a_withdrawal_word_in_the_same_sentence(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · test_weakened — x\n'),
            _review(2, 'PASS', '## 上一轮发现逐条核对\n\n- **r1#1 test_weakened（u9 的红是缺符号的红）**：已修。442780fd 只撤回了 `selected_problem` 里的条件。\n'))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')

    def test_owner_words_in_a_later_explanation_do_not_make_it_owner(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · test_weakened — x\n'),
            _review(2, 'FAIL', (
                '## 发现\n\n### #1 blocking · test_weakened — y\n\n'
                '| r1#1 blocking test_weakened (AC7) | **残留（范围缩小）→ 本轮 #1** | 某段说明 owner 消息 2026-11-15 的要求。 |\n')),
            _review(3, 'PASS', (
                '## 上一轮发现逐条核对\n\n'
                '| r2#1 blocking test_weakened (AC7，r1#1 的残留) | **已修** | 某段说明 owner 消息 2026-11-16 的要求。 |\n')))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')
        self.assertEqual(chain['rounds'], [1, 2])

    def test_a_facts_table_mention_does_not_override_the_disposition(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · bug — x\n'),
            _review(2, 'PASS', (
                '## 上一轮发现逐条核对\n\n'
                '- **r1#1 `bug` — 已修。** 见下。\n\n'
                '| `artifacts/facts/build.json` | pass | r1#1 已修，不构成削弱 |\n')))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')

    def test_last_mention_still_open_is_its_own_reason(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · bug — x\n'),
            _review(2, 'FAIL', '## 发现\n\n### #1 blocking · bug — x\n\nr1#1 仍在（本轮 #1）\n'))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'unknown')
        self.assertEqual(chain['reason'], 'last mention still open')

    def test_rebutted_and_owner_and_fixed(self):
        versions = _versions(
            _review(1, 'FAIL', (
                '## 发现\n\n### #1 blocking · bug — a\n\n'
                '### #2 blocking · ac_gap — b\n\n'
                '### #3 blocking · test_weakened — c\n')),
            _review(2, 'PASS', (
                '## 上一轮发现逐条核对\n\n'
                '- **r1#1 已修。**\n\n'
                '- **r1#2：撤回（build 证据成立）。**\n\n'
                '- **r1#3：撤回（owner 消息 2026-11-14 明确接受）。**\n')))
        got = {c['item']: (c['outcome'], c['reason']) for c in rf.chain_findings(self.cap, versions)}
        self.assertEqual(got[1], ('fixed', ''))
        self.assertEqual(got[2], ('rebutted', ''))
        self.assertEqual(got[3], ('owner', ''))

    def test_summary_counts_and_n_a_class(self):
        findings = [
            {'class': 'bug', 'model': 'M1', 'outcome': 'fixed', 'reason': '', 'rounds': [1]},
            {'class': 'test_weakened', 'model': 'M1', 'outcome': 'rebutted', 'reason': '', 'rounds': [1]},
            {'class': 'ac_gap', 'model': 'M2', 'outcome': 'owner', 'reason': '', 'rounds': [1]},
            {'class': 'bug', 'model': 'M1', 'outcome': 'unknown', 'reason': 'no later round', 'rounds': [1]},
            {'class': 'guarantee', 'model': 'M1', 'outcome': 'fixed', 'reason': '', 'rounds': [1, 2]},
            {'class': 'test_weakened', 'model': 'M1', 'outcome': 'fixed', 'reason': '', 'rounds': [1]},
        ]
        data = rf.summarize_findings(findings, 6, 3, 3, 1, items_n=7)
        self.assertEqual(data['items'], 7)
        self.assertEqual(data['chains'], 6)
        self.assertEqual(data['cross_round'], 1)
        self.assertEqual(data['decidable'], 5)
        self.assertEqual(data['suspected'], 2)
        self.assertEqual(data['classes']['bug'], {'items': 2, 'decidable': 1, 'suspected': 0})
        self.assertEqual(data['models']['M1'], {'items': 5, 'decidable': 4, 'suspected': 1})
        self.assertEqual(data['reasons']['no later round'], 1)
        import io
        out = io.StringIO()
        rf.blocking_lines(data, out)
        self.assertIn('  project_rule: items 0, decidable 0, suspected n/a', out.getvalue())
        self.assertIn('  bug: items 2, decidable 1, suspected 0/1 (0%)', out.getvalue())


class DocstringCaliberTest(unittest.TestCase):
    """AC1: the caliber lives in the tool's module docstring."""

    def test_docstring_names_the_caliber(self):
        doc = rf.__doc__ or ''
        for phrase in (
                'blocking-findings block',
                'artifacts/review.md',
                'git history',
                'stamped hand-off',
                'bug, ac_gap, project_rule,',
                'unclassified',
                '`r<n>#<k>`',
                'suspected false-positive rate',
                'numerator',
                'denominator',
                'unknown',
                'one model',
                'fixed but no behaviour change',
                '<csv>.blocking.csv',
        ):
            self.assertIn(phrase, doc, 'the docstring must state: ' + phrase)


if __name__ == '__main__':
    unittest.main()