"""Classification flows on one shared isolated server: plan cases 1-4 and 12.

Each case performs every mutation first and observes afterwards, so harness
faults surface as errors now rather than hiding behind the expected
FeatureAbsent. Expected values follow plan D2 and test/neglect/FAILURES.md.
"""
import sys
import uuid

from contract import Checks, activity, ids, listing, neglected, task_activity
from harness import CLI, parse

sys.path.insert(0, str(CLI.parent))
import helm_focus_activity as v1  # noqa: E402


def _v1(w, task, status, at=None, source=None):
    """Fixture self-check: v1 over the real server timeline agrees with the premise."""
    value = v1.activity_evidence(w.get(task), w.timeline(task), complete=True)
    assert value['status'] == status, value
    if at == 'fresh':
        assert parse(value['at']) >= w.clock.t0, value
    elif at:
        assert value['at'] == at, value
    if source:
        assert value['source'] == source, value
    return value


def _fresh(clock, value):
    at = parse(value)
    assert at is not None and at >= clock.t0, ('expected activity after T0', value)
    return value


def case_01_legacy_history(ctx, receipt):
    w, clock = ctx.world, ctx.clock
    old, recent = w.task('Synthetic legacy-only card'), w.task('Synthetic recently legacy-edited card')
    for task, days in ((old, 60), (recent, 1)):
        w.patch(task, priority='high')
        with w.s.db() as db:
            # Proxy for a pre-1c54493 binary: identical row minus changed_fields.
            db.execute("UPDATE events SET payload=json_remove(payload, '$.changed_fields') "
                       "WHERE task_id=? AND type='task.updated'", (task['id'],))
            db.execute('UPDATE tasks SET created_at=? WHERE id=?', (clock.ago(61), task['id']))
            db.execute("UPDATE events SET created_at=? WHERE task_id=? AND type='task.created'",
                       (clock.ago(61), task['id']))
            db.execute("UPDATE events SET created_at=? WHERE task_id=? AND type='task.updated'",
                       (clock.ago(days), task['id']))
        w.touch(task)
    receipt['fixture'] = {'created': 'T0-61d', 'legacy_update': {'old': 'T0-60d', 'recent': 'T0-1d'}}
    checks = Checks(receipt)

    def old_bounded():
        value = task_activity(w, old)
        assert value['status'] == 'bounded' and value['reason'] == 'legacy_update', value
        assert value['not_after'] == clock.ago(60), value
        return value

    def recent_bounded():
        value = task_activity(w, recent)
        assert value['status'] == 'bounded' and value['not_after'] == clock.ago(1), value
        return value

    def inactive_listing():
        tasks, pages = neglected(w.s, w.key, 14)
        assert old['id'] in ids(tasks), 'legacy card 60 days old missing from inactive_days=14'
        assert recent['id'] not in ids(tasks), 'bounded card inside window reported inactive'
        return {'ids': ids(tasks), 'pages': pages}

    def undetermined_listing():
        tasks, _ = neglected(w.s, w.key, 14, activity_status='undetermined')
        assert recent['id'] in ids(tasks) and old['id'] not in ids(tasks), ids(tasks)
        return ids(tasks)

    checks.check('fixture_v1_old_is_unknown', lambda: _v1(w, old, 'unknown'))
    checks.check('fixture_v1_recent_is_unknown', lambda: _v1(w, recent, 'unknown'))
    checks.check('legacy_60d_is_bounded', old_bounded)
    checks.check('legacy_1d_is_bounded', recent_bounded)
    checks.check('inactive_days_14_includes_bounded_old_only', inactive_listing)
    checks.check('recent_bounded_is_undetermined_not_active', undetermined_listing)
    checks.finish()


def case_02_noise_does_not_refresh(ctx, receipt):
    w, clock = ctx.world, ctx.clock
    focus = w.s.api('POST', f'/projects/{w.key}/releases', {'name': 'Synthetic noise Focus'}, expected=201)
    task = w.task('Synthetic noisy old card')
    items = w.checklist_add(task, 'Synthetic step one')
    items = w.checklist_add(task, 'Synthetic step two')
    w.action(task, 'claim', token=w.token)
    operation = str(uuid.uuid4())
    w.progress(task, operation, 'Build', 1)
    w.backdate(task, clock.ago(21))
    # The agent still holds its claim; every noise step below appends events
    # at new cursors, so no separate touch is needed.
    steps = []
    w.heartbeat(task, operation); steps.append('heartbeat')
    w.action(task, 'renew', token=w.token); steps.append('claim_renewed')
    w.progress(task, str(uuid.uuid4()), 'Build', 1); steps.append('identical_progress')
    w.comment(task, 'Synthetic agent status note', token=w.token); steps.append('agent_comment')
    w.action(task, 'release', token=w.token); steps.append('claim_released')
    w.action(task, 'claim'); w.action(task, 'release'); steps.append('human_claim_release')
    w.patch(task, assignee=w.agent_id); steps.append('assignment')
    w.patch(task, priority='urgent'); steps.append('metadata_only_update')
    w.patch(task, release_id=focus['id']); steps.append('focus_membership')
    w.checklist_reorder(task, [item['id'] for item in reversed(items)]); steps.append('checklist_reorder')
    receipt['fixture'] = {'history_at': 'T0-21d', 'noise_steps': steps}
    checks = Checks(receipt)

    def unchanged():
        value = task_activity(w, task)
        assert value['status'] == 'known', value
        assert value['not_after'] == clock.ago(21), ('noise refreshed the clock', value)
        return value

    def still_inactive():
        tasks, _ = neglected(w.s, w.key, 14)
        assert task['id'] in ids(tasks), 'noisy 21-day card missing from inactive_days=14'
        return True

    checks.check('fixture_v1_noise_ignored', lambda: _v1(w, task, 'known', clock.ago(21)))
    checks.check('not_after_unchanged_after_all_noise', unchanged)
    checks.check('noisy_card_still_inactive', still_inactive)
    checks.finish()


def case_03_real_activity_refreshes(ctx, receipt):
    w, clock = ctx.world, ctx.clock
    cards = {name: w.task(f'Synthetic refresh by {name}') for name in
             ('human_comment', 'scope_edit', 'column_move', 'checklist_completion',
              'dependency_change', 'progress_change')}
    item = w.checklist_add(cards['checklist_completion'], 'Synthetic finish step')[0]
    prerequisite = w.task('Synthetic prerequisite')
    progress_card = cards['progress_change']
    w.action(progress_card, 'claim', token=w.token)
    operation = str(uuid.uuid4())
    w.progress(progress_card, operation, 'Build', 1)
    for task in cards.values():
        w.backdate(task, clock.ago(21))
        if task is not progress_card:
            w.touch(task)
    w.comment(cards['human_comment'], 'Synthetic acceptance clarified')
    w.patch(cards['scope_edit'], description='Acceptance: synthetic finish condition')
    w.move(cards['column_move'], 'ready')
    w.checklist_set(cards['checklist_completion'], item['id'], True)
    w.depend(cards['dependency_change'], prerequisite)
    w.progress(progress_card, operation, 'Verify', 2)
    expected = {'human_comment': 'human.comment', 'scope_edit': 'task.scope_or_state_changed',
                'column_move': 'task.moved', 'checklist_completion': 'task.checklist_completed',
                'dependency_change': 'task.dependency_added', 'progress_change': 'agent.progress_changed'}
    receipt['fixture'] = {'history_at': 'T0-21d', 'expected_sources': expected}
    checks = Checks(receipt)
    for name, task in cards.items():
        checks.check(f'fixture_v1_{name}', lambda task=task, name=name: _v1(w, task, 'known', 'fresh', expected[name]))

        def known_and_current(task=task, source=expected[name]):
            value = task_activity(w, task)
            assert value['status'] == 'known' and value['source'] == source, value
            _fresh(clock, value['not_after'])
            return value
        checks.check(f'{name}_is_known_and_current', known_and_current)

    def none_inactive():
        tasks, _ = neglected(w.s, w.key, 14)
        stale = set(ids(tasks)) & {task['id'] for task in cards.values()}
        assert not stale, ('refreshed cards still inactive', sorted(stale))
        return True
    checks.check('refreshed_cards_leave_inactive_listing', none_inactive)
    checks.finish()


def case_04_read_your_writes(ctx, receipt):
    w, clock = ctx.world, ctx.clock
    task = w.old_task('Synthetic read-your-writes card', 30)
    w.comment(task, 'Synthetic fresh human comment')
    # No settle: the first answer after the write is the one under test.
    checks = Checks(receipt)

    def first_answer():
        _, body = listing(w.s, w.key, inactive_days=14, limit=200)
        coverage = body['activity_coverage']
        row = next((t for t in body['data'] if t['id'] == task['id']), None)
        if row is not None:
            value = activity(row)
            assert value['status'] == 'pending' and coverage['complete'] is False, \
                ('fresh card returned as inactive', value, coverage)
        return {'returned': row is not None, 'coverage': coverage}

    checks.check('fresh_comment_never_listed_inactive', first_answer)
    checks.check('task_read_is_current_or_pending', lambda: _fresh(clock, task_activity(w, task)['not_after']))
    checks.finish()


def case_12_comment_removal(ctx, receipt):
    w, clock = ctx.world, ctx.clock
    task = w.task('Synthetic comment removal card')
    comment = w.comment(task, 'Synthetic human evidence')
    w.backdate(task, clock.ago(40))
    with w.s.db() as db:
        db.execute('UPDATE comments SET created_at=?, updated_at=? WHERE id=?',
                   (clock.ago(20), clock.ago(20), comment['id']))
        db.execute("UPDATE events SET created_at=? WHERE task_id=? AND type='comment.created'",
                   (clock.ago(20), task['id']))
    w.touch(task)
    checks = Checks(receipt)
    checks.check('fixture_v1_comment_is_last', lambda: _v1(w, task, 'known', clock.ago(20), 'human.comment'))
    before = checks.check('comment_is_last_activity', lambda: _expect(
        task_activity(w, task), 'known', clock.ago(20), 'human.comment'))
    w.s.api('DELETE', f'/tasks/{task["id"]}/comments/{comment["id"]}', version=comment['version'], expected=204)
    timeline = [(row['kind'], (row.get('change') or {}).get('event_type')) for row in w.timeline(task)]
    receipt['fixture'] = {'created': 'T0-40d', 'comment': 'T0-20d', 'timeline_after_delete': timeline,
                          'before_delete': before is not None}
    checks.check('fixture_v1_after_delete', lambda: _v1(w, task, 'unknown'))
    checks.check('falls_back_to_previous_evidence', lambda: _expect(
        task_activity(w, task), 'known', clock.ago(40), 'task.created'))
    checks.finish()


def _expect(value, status, not_after, source=None):
    assert value['status'] == status and value['not_after'] == not_after, (status, not_after, value)
    if source:
        assert value['source'] == source, value
    return value


CASES = {1: case_01_legacy_history, 2: case_02_noise_does_not_refresh,
         3: case_03_real_activity_refreshes, 4: case_04_read_your_writes,
         12: case_12_comment_removal}
