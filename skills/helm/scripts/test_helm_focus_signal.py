"""Isolation tests for focus review on the server neglect signal (TC-381).

Failure modes are recorded first in test/neglect/CLI.md; real-server flows are
neglect cases 17 to 20. These pin the pure rules: the threshold instant, the
bounded/undetermined/pending meanings, coverage aggregation, and cursors that
stay byte-identical to version 1 when the server has no signal.
"""
from datetime import datetime, timezone
import base64
import json
import unittest

from helm_config import HelmError
from helm_focus_reader import read_cursor, write_cursor
from helm_focus_review import _findings
from helm_focus_signal import SERVER_MAX_DAYS, Signal, listing_supports, supported, task_supports

E = '2026-11-08T00:00:00Z'


def server(status, not_after=None, source='', reason='', version=2):
    return {'status': status, 'last_meaningful_at': not_after if status == 'known' else None,
            'source': source, 'not_after': not_after, 'reason': reason,
            'classification_version': None if status == 'pending' else version}


def page(evaluated_at=E, complete=True, lag=0, pending=0):
    return {'data': [], 'evaluated_at': evaluated_at,
            'activity_coverage': {'complete': complete, 'lag': lag, 'pending': pending}}


class AssessmentTests(unittest.TestCase):
    def setUp(self):
        self.signal = Signal(14)
        self.signal.observe(page())

    def assess(self, activity):
        return self.signal.assess({'id': 't', 'key': 'K-1', 'activity': activity})

    def test_threshold_instant_is_inactive_and_one_millisecond_later_is_not(self):
        at = self.assess(server('known', '2026-10-25T00:00:00Z', 'human.comment'))
        later = self.assess(server('known', '2026-10-25T00:00:00.001Z', 'human.comment'))
        self.assertEqual(at['assessment'], 'inactive')
        self.assertEqual(later['assessment'], 'active')

    def test_bounded_inside_window_is_undetermined_never_active(self):
        value = self.assess(server('bounded', '2026-11-07T00:00:00Z', reason='legacy_update'))
        self.assertEqual(value['assessment'], 'undetermined')
        self.assertEqual(value['status'], 'bounded')
        self.assertIsNone(value['at'])

    def test_bounded_past_threshold_is_inactive(self):
        value = self.assess(server('bounded', '2026-09-09T00:00:00Z', reason='legacy_update'))
        self.assertEqual(value['assessment'], 'inactive')
        self.assertEqual(value['not_after'], '2026-09-09T00:00:00Z')

    def test_pending_is_neither_active_nor_inactive(self):
        value = self.assess(server('pending'))
        self.assertEqual(value['assessment'], 'pending')
        self.assertEqual(value['status'], 'pending')

    def test_unknown_and_unrecognised_statuses_stay_unknown(self):
        self.assertEqual(self.assess(server('unknown', reason='unparseable_time'))['assessment'], 'unknown')
        self.assertEqual(self.assess(server('mystery', '2026-11-07T00:00:00Z'))['assessment'], 'unknown')
        self.assertEqual(self.assess(server('known', None, 'task.created'))['assessment'], 'unknown')

    def test_trimmed_fractions_compare_as_instants(self):
        signal = Signal(1)
        signal.observe(page('2026-11-08T00:00:00.5Z'))
        early = signal.assess({'activity': server('known', '2026-11-07T00:00:00.25Z', 'human.comment')})
        late = signal.assess({'activity': server('known', '2026-11-07T00:00:00.75Z', 'human.comment')})
        self.assertEqual((early['assessment'], late['assessment']), ('inactive', 'active'))

    def test_server_instant_governs_not_the_client_clock(self):
        signal = Signal(14)
        signal.observe(page('2020-01-20T00:00:00Z'))
        value = signal.assess({'activity': server('known', '2020-01-10T00:00:00Z', 'task.created')})
        self.assertEqual(value['assessment'], 'active')
        self.assertEqual(signal.summary()['evaluated_at'], '2020-01-20T00:00:00Z')

    def test_first_page_instant_is_kept_for_the_invocation(self):
        signal = Signal(14)
        signal.observe(page('2026-11-08T00:00:00Z'))
        signal.observe(page('2026-11-08T00:00:01Z'))
        self.assertEqual(signal.summary()['evaluated_at'], '2026-11-08T00:00:00Z')

    def test_no_server_instant_cannot_conclude(self):
        value = Signal(14).assess({'activity': server('known', '2020-01-01T00:00:00Z', 'task.created')})
        self.assertEqual(value['assessment'], 'pending')

    def test_output_keeps_version_1_keys_and_adds_bound(self):
        value = self.assess(server('known', '2026-10-01T00:00:00Z', 'human.comment'))
        self.assertTrue({'status', 'at', 'source', 'classification_version', 'not_after'} <= set(value))
        self.assertEqual(value['at'], '2026-10-01T00:00:00Z')
        self.assertEqual(value['classification_version'], 2)


class CoverageTests(unittest.TestCase):
    def test_incomplete_page_makes_the_review_partial(self):
        signal = Signal(14)
        signal.observe(page(lag=0))
        signal.observe(page(complete=False, lag=7, pending=2))
        summary = signal.summary()
        self.assertEqual(summary['activity_coverage'], {'complete': False, 'lag': 7, 'pending': 2})
        self.assertEqual(summary['path'], 'server')
        self.assertTrue(signal.partial)

    def test_complete_pages_are_not_partial(self):
        signal = Signal(14)
        signal.observe(page())
        self.assertFalse(signal.partial)
        self.assertEqual(signal.summary()['unused_limits'], ['timeline_limit'])

    def test_missing_coverage_is_not_complete(self):
        signal = Signal(14)
        signal.observe({'evaluated_at': E, 'activity_coverage': {}})
        self.assertTrue(signal.partial)


class DetectionTests(unittest.TestCase):
    def test_server_range(self):
        self.assertEqual(SERVER_MAX_DAYS, 3650)
        self.assertTrue(supported(3650))
        self.assertFalse(supported(3651))

    def test_listing_and_task_detection(self):
        self.assertTrue(listing_supports(page()))
        self.assertFalse(listing_supports({'data': [], 'next_cursor': ''}))
        self.assertTrue(task_supports({'activity': server('pending')}))
        self.assertFalse(task_supports({'id': 't'}))


class CursorTests(unittest.TestCase):
    def test_version_1_cursor_bytes_are_unchanged(self):
        raw = write_cursor('p', '', offset=0, focus_after='', backlog_after='!')
        expected = base64.urlsafe_b64encode(json.dumps(
            {'v': 1, 'project': 'p', 'focus': '', 'offset': 0, 'focus_after': '', 'backlog_after': '!'},
            separators=(',', ':')).encode()).decode()
        self.assertEqual(raw, expected)

    def test_server_cursor_round_trips_and_validates(self):
        raw = write_cursor('p', '', offset=1, signal=1, undetermined_after='abc')
        value = read_cursor(raw, 'p', '')
        self.assertEqual((value['signal'], value['undetermined_after']), (1, 'abc'))
        for bad in ({'signal': 'yes'}, {'signal': 1, 'undetermined_after': 3}):
            with self.assertRaises(HelmError):
                read_cursor(write_cursor('p', '', **bad), 'p', '')


class FindingTests(unittest.TestCase):
    NOW = datetime(2026, 11, 8, tzinfo=timezone.utc)
    TASK = {'id': 't', 'key': 'K-1', 'assignee': 'a', 'agent_work': {'next_action': 'x'},
            'checklist_summary': {'total': 1}}

    def reasons(self, assessment, status='known'):
        activity = {'status': status, 'at': None, 'source': None, 'not_after': '2026-09-01T00:00:00Z',
                    'reason': None, 'classification_version': 2, 'assessment': assessment}
        rows = _findings(self.TASK, activity, self.NOW, 14, 'ready', None)
        return {row['reason']: row for row in rows}

    def test_each_assessment_has_one_honest_finding(self):
        self.assertEqual(self.reasons('inactive', 'bounded')['inactive']['at'], '2026-09-01T00:00:00Z')
        self.assertEqual(set(self.reasons('undetermined', 'bounded')), {'activity_undetermined'})
        self.assertEqual(set(self.reasons('pending', 'pending')), {'activity_pending'})
        self.assertEqual(set(self.reasons('unknown', 'unknown')), {'activity_unknown'})
        self.assertEqual(set(self.reasons('active')), set())


if __name__ == '__main__':
    unittest.main()
