"""Failure-first classification and bounded review tests; see test/focus/FAILURES.md.

Isolation is needed for historical clocks, legacy payloads and identical pulses.
Real API behavior is additionally exercised by test/focus/run.py.
"""
import unittest

from helm_focus_activity import activity_evidence


class ActivityTests(unittest.TestCase):
    def setUp(self):
        self.task = {'id': 't', 'created_at': '2026-10-01T00:00:00Z'}

    def row(self, kind, date, **payload):
        return {'kind': kind, 'created_at': date, **payload}

    def test_heartbeat_lease_and_unknown_patch_do_not_make_work_fresh(self):
        rows = [self.row('task_change', '2026-11-07T00:00:00Z', change={
            'event_type': event, 'payload': {'version': 2}})
            for event in ('task.claimed', 'task.heartbeat', 'task.updated')]
        value = activity_evidence(self.task, rows, complete=True)
        self.assertEqual(value['status'], 'unknown')
        self.assertIsNone(value['at'])

    def test_complete_history_uses_creation(self):
        value = activity_evidence(self.task, [], complete=True)
        self.assertEqual(value['at'], self.task['created_at'])
        self.assertEqual(value['source'], 'task.created')

    def test_identical_pulses_do_not_refresh(self):
        pulse = {'phase': 'Build', 'checkpoint_completed': 1, 'checkpoint_total': 3,
                 'checkpoint_refs': [], 'next_action': 'Verify'}
        rows = [self.row('agent_progress', at, progress=pulse) for at in
                ['2026-11-07T00:00:00Z', '2026-10-02T00:00:00Z']]
        value = activity_evidence(self.task, rows, complete=True)
        self.assertEqual(value['at'], '2026-10-02T00:00:00Z')

    def test_changed_progress_and_human_comment_count(self):
        rows = [self.row('comment', '2026-11-04T00:00:00Z',
                         actor={'kind': 'agent'}, comment={'body': 'still here'}),
                self.row('comment', '2026-11-03T00:00:00Z',
                         actor={'kind': 'human'}, comment={'body': 'acceptance updated'})]
        self.assertEqual(activity_evidence(self.task, rows, complete=True)['at'],
                         '2026-11-03T00:00:00Z')

    def test_truncated_history_is_a_bound_not_a_fake_date(self):
        rows = [self.row('agent_progress', '2026-11-07T00:00:00Z', progress={'phase': 'A'})]
        value = activity_evidence(self.task, rows, complete=False)
        self.assertEqual(value['status'], 'unknown')
        self.assertIsNone(value['at'])

    def test_reorder_is_not_state_change(self):
        row = self.row('task_change', '2026-11-07T00:00:00Z', change={
            'event_type': 'task.moved', 'payload': {'from_column_id': 'a', 'to_column_id': 'a'}})
        self.assertEqual(activity_evidence(self.task, [row], complete=True)['at'],
                         self.task['created_at'])

    def test_dependency_and_checklist_completion_count(self):
        row = self.row('task_change', '2026-11-01T00:00:00Z', change={
            'event_type': 'task.checklist_item_updated', 'payload': {'completed': True}})
        self.assertEqual(activity_evidence(self.task, [row], complete=True)['at'], row['created_at'])

    def test_deleted_comment_actor_is_unknown(self):
        row = self.row('comment', '2026-11-07T00:00:00Z', actor=None, comment={'body': 'note'})
        self.assertEqual(activity_evidence(self.task, [row], complete=True)['status'], 'unknown')

    def test_new_evidence_attachments_count(self):
        rows = [self.row('agent_progress', '2026-11-07T00:00:00Z', progress={
            'phase': 'Review', 'attachments': [{'id': 'new-evidence'}]}),
            self.row('agent_progress', '2026-10-01T00:00:00Z', progress={'phase': 'Review'})]
        self.assertEqual(activity_evidence(self.task, rows, complete=True)['at'], rows[0]['created_at'])

    def test_legacy_mixed_scope_assignment_is_unknown(self):
        row = self.row('task_change', '2026-11-07T00:00:00Z', change={
            'event_type': 'task.updated', 'payload': {'assignment_changed': True, 'version': 2}})
        self.assertEqual(activity_evidence(self.task, [row], complete=True)['status'], 'unknown')

    def test_explicit_metadata_only_update_does_not_reset_activity(self):
        row = self.row('task_change', '2026-11-07T00:00:00Z', change={
            'event_type': 'task.updated', 'payload': {'changed_fields': [], 'version': 2}})
        self.assertEqual(activity_evidence(self.task, [row], complete=True)['at'], self.task['created_at'])

    def test_fractional_timestamps_sort_chronologically(self):
        rows = [self.row('agent_progress', '2026-11-07T00:00:00.1Z', progress={'phase': 'B'}),
                self.row('agent_progress', '2026-11-07T00:00:00Z', progress={'phase': 'A'})]
        self.assertEqual(activity_evidence(self.task, rows, complete=True)['at'], rows[0]['created_at'])


if __name__ == '__main__':
    unittest.main()
