"""Unit tests for tools/review-firstpass (polyforge#242).

The tool has no .py suffix; it is loaded by path. The cases are the ticket's
fixture table (polyforge#242, 分析 2): the cross-round reference chain, the
owner's asks.jsonl rulings (entry numbers only, never inferred from prose),
the class/model summary, and the unknown-outcome reasons. Expected values are
hand-derived from the ticket, never read out of the tool.

polyforge#261 adds the delivery-metric regressions: the rework measure counts
each dispatch of overlapping rewind windows once and subtracts a coordinator
pause (`job_finished{reason: paused}` to the next `job_started`), and the
estimate line ignores a description line, a fenced code block and the
`## 历次分析` archive.

Run from the repository root:
  python3 -m unittest discover -s tools -p 'test_review_firstpass*.py'
"""

import importlib.machinery
import importlib.util
import json
import os
import re
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
    out = []
    for i, t in enumerate(texts):
        rnd = rf.review_round(t) or i + 1
        out.append({'round': rnd, 'verdict': rf.review_verdict(t), 'text': t,
                    'items': rf.parse_blocking_items(t, rnd)})
    return out


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

    def test_negated_blocking_is_not_an_item(self):
        text = _review(1, 'FAIL', '## 发现\n\n4. 顺手可做（不阻断）：#9 和 #13 的措辞\n')
        self.assertEqual(_item(text, 1), [])

    def test_a_section_heading_without_blocking_is_not_a_blocking_section(self):
        text = _review(1, 'FAIL', '## 逐项核对（无阻断的部分）\n\n  #24 下会把唯一块折叠\n')
        self.assertEqual(_item(text, 1), [])

    def test_a_body_number_list_under_discovery_is_not_an_item(self):
        text = _review(1, 'FAIL', (
            '## 发现\n\n### #1 blocking · guarantee — x\n\n'
            '2. 本改动自己的过渡没有交代，见 ac_gap 的说明。\n'))
        self.assertEqual([(i['item'], i['class']) for i in _item(text, 1)],
                         [(1, 'guarantee')])

    def test_non_blocking_marker_is_not_an_item(self):
        text = _review(1, 'FAIL', '## 发现\n\n- **#1 非阻断**：plan-conformance 把一处说明当成阻断。\n')
        self.assertEqual(_item(text, 1), [])

    def test_a_non_blocking_count_in_a_bullet_is_not_an_item(self):
        text = _review(1, 'FAIL', (
            '## 发现\n\n'
            '- **#227** https://example.invalid/x（MERGED，review 第 4 轮 PASS，7 条非阻断）：说明。\n'))
        self.assertEqual(_item(text, 1), [])

    def test_a_number_range_placeholder_is_not_an_item(self):
        text = _review(1, 'FAIL', (
            '## 发现\n\n'
            '### #2–#5 —（编号保留给 r1 的 4 条阻断，均已修，见上表）\n'))
        self.assertEqual(_item(text, 1), [])

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
                '## 发现\n\n### #1 blocking · test_weakened（残留 r1#1）— y\n\n'
                '| r1#1 blocking test_weakened (AC7) | **残留（范围缩小）** | 某段说明 owner 消息 2026-11-15 的要求。 |\n')),
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
            _review(2, 'FAIL', '## 发现\n\n### #1 blocking · bug（残留 r1#1）— x\n\nr1#1 仍在\n'))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'unknown')
        self.assertEqual(chain['reason'], 'last mention still open')

    def test_a_table_row_uses_its_first_status_cell(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #2 blocking · test_weakened — x\n'),
            _review(2, 'PASS', (
                '## 上一轮发现逐条核对\n\n'
                '| r1#2 信任写入绕过 | 已修（行为） | 另外一条路由没有改成调用它，见 #3（非阻断）。 |\n')))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')

    def test_a_table_row_whose_first_cell_is_backticked_is_a_disposition(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · bug — x\n'),
            _review(2, 'PASS', '## 上一轮发现逐条核对\n\n| `r1#1` | **已修** | 说明 |\n'))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')

    def test_a_numbered_list_and_an_ascii_semicolon_are_dispositions(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · bug — x\n\n### #2 blocking · test_weakened — y\n'),
            _review(2, 'PASS', '## 上一轮发现逐条核对\n\n1. **r1#1 已修**; r1#2 撤回（build 证据成立）\n'))
        got = {c['item']: c['outcome'] for c in rf.chain_findings(self.cap, versions)}
        self.assertEqual(got[1], 'fixed')
        self.assertEqual(got[2], 'rebutted')

    def test_this_round_reference_links_the_chain(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #1 blocking · guarantee — x\n'),
            _review(2, 'FAIL', '## 发现\n\n### #1 blocking · guarantee — x\n\nr1#1 仍在（本轮 #1）\n'),
            _review(3, 'PASS', '## 上一轮发现逐条核对\n\n- **r2#1 已修。**\n'))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')
        self.assertEqual(chain['rounds'], [1, 2])

    def test_owner_ruling_without_a_withdrawal_word(self):
        cases = [
            ('r1#2 ac_gap AC1 —— owner 已裁定，不再提。', 'owner'),
            ('| r1#4 guarantee AC3 | **已由 owner 裁定** | 说明 |', 'owner'),
            ('r1#1（blocking · test_weakened · u1）：**owner 裁定已处理，不再提出。**', 'owner'),
            ('r1#3 guarantee（自定非目标）：已按 owner 裁定修。', 'fixed'),
        ]
        for line, want in cases:
            versions = _versions(
                _review(1, 'FAIL', '## 发现\n\n### #%s blocking · guarantee — x\n'
                        % line.split('#', 1)[1].split(' ', 1)[0]),
                _review(2, 'PASS', '## 上一轮发现逐条核对\n\n' + line + '\n'))
            [chain] = rf.chain_findings(self.cap, versions)
            self.assertEqual(chain['outcome'], want, line)

    def test_a_negated_owner_ruling_is_not_a_ruling(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #2 blocking · bug — x\n'),
            _review(2, 'PASS', (
                '## 上一轮发现逐条核对\n\n'
                '| r1#2 bug | **已修** | 说明 |\n\n'
                '- owner 消息：build 第 2 轮 header 记「无」。r1#2 的修法用的是 review 推荐的软链，没有新的目的地，不需要 owner 裁定。\n')))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'fixed')

    def test_a_still_open_disposition_is_not_reported_as_a_pass_without_naming(self):
        versions = _versions(
            _review(1, 'FAIL', '## 发现\n\n### #2 blocking · ac_gap — x\n'),
            _review(2, 'PASS', '## 上一轮发现逐条核对\n\n| r1#2 ac_gap | **残留**（见 #1） | 说明 |\n'),
            _review(3, 'PASS', '## 结论\n\nPASS。\n'))
        [chain] = rf.chain_findings(self.cap, versions)
        self.assertEqual(chain['outcome'], 'unknown')
        self.assertEqual(chain['reason'], 'last mention still open')

    def test_owner_accepts_and_reclassifies_is_owner(self):
        rows = [
            '| r5#1 ac_gap AC1 | owner「b」接受（child argv 截获、operator 删除刷新）→ 非阻断 #6 |',
            '| r5#2 ac_gap AC2 | owner「b」接受（on 正向 / 非法值）→ 非阻断 #6 |',
            '| r5#6 test_weakened sequential 文本测试 | owner「b」接受（sequential 行为测试）→ 非阻断 #6 |',
            '| r5#7 test_weakened 伪 test-first | owner「b」接受（units.json 真实测试提交关系）→ 非阻断 #6 |',
            '| r5#9 test_weakened toggle 测试不属单元 | 归入 owner 接受的「units.json 真实测试提交关系」→ 非阻断 #6 |',
        ]
        for row in rows:
            item = re.search(r'r5#(\d+)', row).group(1)
            versions = _versions(
                _review(5, 'FAIL', '## 发现\n\n### #%s blocking · ac_gap — x\n' % item),
                _review(6, 'PASS', '## 上一轮发现逐条核对\n\n' + row + '\n'))
            [chain] = rf.chain_findings(self.cap, versions)
            self.assertEqual(chain['outcome'], 'owner', row)

    def test_a_pending_owner_decision_is_not_a_ruling(self):
        cases = [
            '| r1#10 | 仍在，非阻断 | follow-up，留 owner 决定 |',
            'r1#3：残留，需要 owner 裁定',
        ]
        for line in cases:
            item = re.search(r'r1#(\d+)', line).group(1)
            versions = _versions(
                _review(1, 'FAIL', '## 发现\n\n### #%s blocking · bug — x\n' % item),
                _review(2, 'PASS', '## 上一轮发现逐条核对\n\n' + line + '\n'))
            [chain] = rf.chain_findings(self.cap, versions)
            self.assertNotEqual(chain['outcome'], 'owner', line)

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
                'owner-overturned',
                'owner 已裁定',
                '已由 owner 裁定',
                'owner 裁定已处理',
                'owner 裁定移出',
                'pending',
                'last mention still open outranks',
        ):
            self.assertIn(phrase, doc, 'the docstring must state: ' + phrase)


class ReworkCaliberTest(unittest.TestCase):
    """AC1 of polyforge#261: overlapping rewind windows count one dispatch
    once, and a coordinator pause inside a dispatch is subtracted.

    Timeline (all 2026-11-10Z), read as the daemon writes it: build 10:00
    -> 10:20, review 10:20 -> 10:30 FAIL, rewind #1; build 10:30 -> 10:40,
    review 10:40 -> 10:50 FAIL, rewind #2; build 10:50 -> 11:00, review
    11:00 starts, the coordinator pauses 11:10 -> 11:30, review resumes and
    ends 11:40 PASS. The two rewind windows ([10:30, 11:40] and [10:50,
    11:40]) overlap; hand-computed, each of the five dispatches they cover
    counts once (10 + 10 + 10 + (30 - 20) + 10 = 50m) and the effective
    time is 100m, so the rework is 50m and never exceeds the effective
    time. The old per-rewind sum double-counts the second window and keeps
    the 20m pause (70 + 50 = 120m, above the 100m effective time)."""

    _KINDS = [
        # (clock, kind, step) — the body each kind carries.
        ('10:00:00', 'job_started', ''),
        ('10:00:00', 'step_started', 'build'),
        ('10:20:00', 'step_done', 'build'),
        ('10:20:00', 'step_started', 'review'),
        ('10:30:00', 'gate_verdict', 'review'),
        ('10:30:00', 'rewind', ''),
        ('10:30:00', 'step_started', 'build'),
        ('10:40:00', 'step_done', 'build'),
        ('10:40:00', 'step_started', 'review'),
        ('10:50:00', 'gate_verdict', 'review'),
        ('10:50:00', 'rewind', ''),
        ('10:50:00', 'step_started', 'build'),
        ('11:00:00', 'step_done', 'build'),
        ('11:00:00', 'step_started', 'review'),
        ('11:10:00', 'job_finished', ''),
        ('11:30:00', 'job_started', ''),
        ('11:30:00', 'step_started', 'review'),
        ('11:40:00', 'step_done', 'review'),
        ('11:40:00', 'terminal', ''),
    ]

    def _events(self):
        cap = tempfile.mkdtemp()
        with open(os.path.join(cap, 'capsule.json'), 'w') as f:
            json.dump({'work_item_id': 'wi_test'}, f)
        events_dir = os.path.join(cap, 'events')
        os.makedirs(events_dir)
        with open(os.path.join(events_dir, 'wi-wi_test.jsonl'), 'w') as f:
            for seq, (clock, kind, step) in enumerate(self._KINDS, start=1):
                if kind == 'rewind':
                    body = {'gate': 'review', 'target': 'build', 'archived': ['build']}
                elif kind == 'job_finished':
                    body = {'reason': 'paused'}
                elif kind == 'terminal':
                    body = {'status': 'completed'}
                elif step:
                    body = {'step': step}
                else:
                    body = {}
                f.write(json.dumps({'seq': seq, 'at': '2026-11-10T' + clock + 'Z',
                                    'kind': kind, 'body': body}) + '\n')
        return rf.load_events(cap, events_dir)

    def test_overlapping_rewinds_and_a_pause(self):
        events = self._events()
        recs = rf.dispatch_records(events, rf.owner_wait_intervals(events))
        rework = rf.rework_s(events, recs)
        # 10 (build 10:30) + 10 (review 10:40) + 10 (build 10:50)
        # + (30 - 20 pause) (review 11:00) + 10 (review 11:30).
        self.assertEqual(rework, 50 * 60)
        self.assertEqual(rf.wall_s(events), 100 * 60)
        self.assertLessEqual(rework, rf.wall_s(events))

class EstimateScopeTest(unittest.TestCase):
    """AC2 of polyforge#261: a description line before the estimate, a fenced
    estimate block and the `## 历次分析` archive never change which estimate
    the ticket carries. The expected 2 + 3 = 5 is the current analysis's own
    strict line, hand-read from the fixture."""

    _CONTENT = (
        '## 分析\n\n### 1. 改动范围\n\n'
        '估算依据：按文件数上限估算。\n'
        '```\n'
        '估算：polyforge 新增 9 个文件、修改 9 个文件、单元 3 个\n'
        '```\n'
        '估算：polyforge 新增 2 个文件、修改 3 个文件、单元 1 个\n'
        '\n## 历次分析\n\n<details><summary>旧分析</summary>\n\n'
        '````markdown\n## 分析\n\n### 1. 改动范围\n\n'
        '估算：polyforge 新增 5 个文件、修改 5 个文件、单元 2 个\n````\n\n'
        '</details>\n\n'
        '估算：polyforge 新增 4 个文件、修改 4 个文件、单元 1 个\n')

    def test_estimate_scope_ignores_descriptions_fences_and_the_archive(self):
        cap = tempfile.mkdtemp()
        os.makedirs(os.path.join(cap, 'runtime-state'))
        with open(os.path.join(cap, 'runtime-state', 'work-item.json'), 'w') as f:
            json.dump({'content': self._CONTENT}, f)
        self.assertEqual(rf.parse_estimate(cap), 5)


if __name__ == '__main__':
    unittest.main()