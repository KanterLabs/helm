"""Real HTTP/CLI Focus acceptance scenarios using owned synthetic data only.

Failure cases are recorded in FAILURES.md before implementation. Callbacks own
transport and assertions: api(method, path, body=None, version=None, expected=200)
returns parsed JSON; cli(*argv, ok=True) enforces success/failure and returns JSON.
The human API callback can finish unclaimed tasks, while the CLI agent exercises
its own claim/progress/heartbeat/legacy-release flow. Only this isolated SQLite
DB is clocked; receipts contain no authentication configuration or token values.
"""
from datetime import datetime, timedelta, timezone
import sqlite3
import time
import uuid


def _operation():
    return str(uuid.uuid4())


def _error(response, expected):
    actual = response.get('error', {}).get('code')
    assert actual in expected, (expected, response)
    return actual


def _membership_snapshot(task):
    return {field: task.get(field) for field in ('id', 'column_id', 'claimed_by', 'claim_expires_at')}


def _activity(review, task_id):
    return next(task['activity'] for task in review['tasks'] if task['id'] == task_id)


def _instant(value):
    return datetime.fromisoformat(str(value).replace('Z', '+00:00'))


def _settled(cli, *argv):
    """Server path (TC-381): a card whose newest event is unprocessed is
    reported `pending` and the review partial; rerun until it settles."""
    for _ in range(40):
        review = cli(*argv)
        if not any(t['activity'].get('assessment') == 'pending' for t in review['tasks']):
            return review
        time.sleep(.25)
    raise AssertionError('review stayed pending')


def _event_count(db_path):
    with sqlite3.connect(db_path) as db:
        return db.execute('SELECT COUNT(*) FROM events').fetchone()[0]


def _progress_journey(api, cli, db_path, project):
    """Old structural progress stays old after repeated snapshots/heartbeats."""
    receipts = []
    scope = cli('focus', 'create', '--project', project['id'], '--name', 'Synthetic pulse evidence')['focus']
    task = api('POST', '/projects/' + project['id'] + '/tasks',
               {'title': 'Synthetic pulse evidence task', 'release_id': scope['id']}, expected=201)
    cli('resume', '--task', task['key'], '--operation-id', _operation())
    progress = ('progress', '--task', task['key'], '--state', 'working', '--phase', 'Build',
                '--completed', '1', '--total', '3', '--next', 'Verify synthetic results',
                '--message', 'Synthetic progress with unchanged structural evidence')
    first_operation, repeated_operation = _operation(), _operation()
    first = cli(*progress, '--operation-id', first_operation)['task']
    old_clock = (datetime.now(timezone.utc) - timedelta(days=21)).isoformat().replace('+00:00', 'Z')
    # Backdate all existing activity for this one synthetic task, including its
    # initial resume state transition. Leave the live claim expiry untouched.
    with sqlite3.connect(db_path) as db:
        db.execute('UPDATE tasks SET created_at=?, updated_at=? WHERE id=?',
                   (old_clock, old_clock, task['id']))
        db.execute('UPDATE events SET created_at=? WHERE task_id=?', (old_clock, task['id']))
        db.execute('UPDATE comments SET created_at=?, updated_at=? WHERE task_id=?',
                   (old_clock, old_clock, task['id']))
        db.execute('UPDATE task_agent_work_history SET created_at=?, started_at=? WHERE task_id=?',
                   (old_clock, old_clock, task['id']))
        db.execute('UPDATE task_agent_work SET updated_at=?, started_at=? WHERE task_id=?',
                   (old_clock, old_clock, task['id']))
    # A different publication identity ensures this is a second durable row,
    # rather than an idempotency replay. The structural snapshot is identical.
    repeated = cli(*progress, '--operation-id', repeated_operation)['task']
    assert repeated['version'] > first['version']
    heartbeat = cli('heartbeat', '--task', task['key'], '--operation-id', repeated_operation)['task']
    assert heartbeat['version'] == repeated['version'], heartbeat
    before = _event_count(db_path)
    old_review = _settled(cli, 'focus', 'review', '--project', project['id'], '--focus', scope['id'])
    assert _event_count(db_path) == before, 'read-only review wrote events'
    assert old_review['activity_signal']['path'] == 'server', old_review
    stale_activity = _activity(old_review, task['id'])
    assert stale_activity['status'] == 'known', stale_activity
    assert _instant(stale_activity['at']) == _instant(old_clock), stale_activity
    assert any(row['task_id'] == task['id'] and row['reason'] == 'inactive'
               for row in old_review['findings']), old_review
    with sqlite3.connect(db_path) as db:
        histories = db.execute('SELECT COUNT(*) FROM task_agent_work_history WHERE task_id=?',
                               (task['id'],)).fetchone()[0]
    assert histories == 2, histories
    receipts.append({'name': 'old_progress_survives_identical_publication_and_heartbeat',
                     'task_key': task['key'], 'fixture_clock': old_clock,
                     'durable_progress_rows': histories, 'heartbeat_version_unchanged': True,
                     'activity': stale_activity, 'review': old_review})

    cli('progress', '--task', task['key'], '--state', 'working', '--phase', 'Verify',
        '--completed', '2', '--total', '3', '--next', 'Finish synthetic verification',
        '--message', 'Synthetic verification advanced', '--operation-id', repeated_operation)
    advanced_review = _settled(cli, 'focus', 'review', '--project', project['id'], '--focus', scope['id'])
    advanced = _activity(advanced_review, task['id'])
    assert advanced['status'] == 'known' and _instant(advanced['at']) > _instant(old_clock), advanced
    assert advanced['source'] == 'agent.progress_changed', advanced
    assert not any(row['task_id'] == task['id'] and row['reason'] == 'inactive'
                   for row in advanced_review['findings']), advanced_review
    # Version 1 truncation (unknown/lower_bound) is neglect case 20; the server
    # path reads no timelines and reports the flag as unused instead.
    truncated = _settled(cli, 'focus', 'review', '--project', project['id'], '--focus', scope['id'],
                         '--timeline-limit', '1')
    assert 'timeline_limit' in truncated['activity_signal']['unused_limits'], truncated
    assert _activity(truncated, task['id']) == advanced, truncated
    receipts.append({'name': 'substantive_progress_and_truncated_history',
                     'task_key': task['key'], 'advanced_activity': advanced,
                     'truncated_review': truncated})

    # Preserve the legacy command's meaning: release the agent claim rather
    # than completing or deleting its product Focus.
    cli('release', '--task', task['key'], '--operation-id', _operation())
    released = api('GET', '/tasks/' + task['id'])
    assert not released.get('claimed_by'), released
    cli('focus', 'unassign', '--project', project['id'], '--focus', scope['id'], '--task', task['key'])
    cli('focus', 'delete', '--project', project['id'], '--focus', scope['id'])
    return receipts


def _mutation_journey(api, cli, project, tasks, backlog):
    """Lifecycle, replay, clearing, conflicts and membership against real APIs."""
    receipts = []
    create_operation = _operation()
    create_args = ('focus', 'create', '--project', project['id'], '--name', 'Synthetic lifecycle',
                   '--description', 'Acceptance: finish the synthetic member', '--target-date', '2027-01-15',
                   '--operation-id', create_operation)
    created = cli(*create_args)
    replayed = cli(*create_args)
    assert replayed == created, (created, replayed)
    focus = created['focus']
    original_id = focus['id']
    edit_operation = _operation()
    edit_args = ('focus', 'edit', '--project', project['id'], '--focus', original_id,
                 '--name', 'Synthetic lifecycle edited', '--version', str(focus['version']),
                 '--operation-id', edit_operation)
    edited = cli(*edit_args)
    edit_replay = cli(*edit_args)
    assert edit_replay == edited, (edited, edit_replay)
    assert edited['focus']['id'] == original_id
    assert edited['focus']['description'] == focus['description']
    assert edited['focus']['target_date'] == focus['target_date']
    stale = cli('focus', 'edit', '--project', project['id'], '--focus', original_id,
                '--description', 'This stale write must not replace scope', '--version', str(focus['version']),
                '--operation-id', _operation(), ok=False)
    _error(stale, {'conflict', 'stale_release'})
    unchanged = api('GET', '/releases/' + original_id)
    assert unchanged['version'] == edited['focus']['version'], (unchanged, edited)
    assert unchanged['description'] == focus['description']
    cleared = cli('focus', 'edit', '--project', project['id'], '--focus', original_id,
                  '--clear-description', '--clear-target-date')
    assert cleared['focus'].get('description') in (None, '')
    assert cleared['focus'].get('target_date') is None
    receipts.append({'name': 'focus_create_edit_clear_idempotent_replay_and_stale_guard',
                     'created': created, 'edited': edited, 'cleared': cleared,
                     'create_replayed_identically': True, 'edit_replayed_identically': True,
                     'stale': stale})

    before = api('GET', '/tasks/' + backlog['id'])
    assign_operation = _operation()
    assigned = cli('focus', 'assign', '--project', project['id'], '--focus', original_id,
                   '--task', backlog['key'], '--task', backlog['key'], '--operation-id', assign_operation)
    assert len(assigned['succeeded']) == 1 and assigned['ok'], assigned
    after = api('GET', '/tasks/' + backlog['id'])
    assert after.get('release_id') == original_id, after
    assert _membership_snapshot(after) == _membership_snapshot(before), (before, after)
    # The repeat is a reconciliation no-op after a previously successful
    # membership update, with no extra task version or membership event.
    assign_replay = cli('focus', 'assign', '--project', project['id'], '--focus', original_id,
                        '--task', backlog['key'], '--operation-id', assign_operation)
    assert assign_replay['succeeded'][0]['changed'] is False, assign_replay
    assert api('GET', '/tasks/' + backlog['id'])['version'] == after['version']
    filtered = api('GET', '/projects/' + project['id'] + '/tasks?release=' + original_id)
    assert [task['id'] for task in filtered['data']] == [backlog['id']], filtered
    incomplete = cli('focus', 'complete', '--project', project['id'], '--focus', original_id, ok=False)
    _error(incomplete, {'release_incomplete'})
    nonempty = cli('focus', 'delete', '--project', project['id'], '--focus', original_id, ok=False)
    _error(nonempty, {'release_has_tasks'})
    receipts.append({'name': 'membership_deduplicates_without_claim_or_column_changes',
                     'assigned': assigned, 'repeated': assign_replay,
                     'before': _membership_snapshot(before), 'after': _membership_snapshot(after),
                     'filtered_task_keys': [task['key'] for task in filtered['data']],
                     'incomplete_complete': incomplete, 'nonempty_delete': nonempty})

    done = api('POST', '/tasks/' + backlog['id'] + '/complete',
               {'comment': 'Synthetic member acceptance verified'}, version=after['version'])
    ready = api('GET', '/releases/' + original_id)
    assert ready['status'] == 'planned' and ready['summary']['ready_to_release'], ready
    completed = cli('focus', 'complete', '--project', project['id'], '--focus', original_id)
    assert completed['focus']['status'] == 'released', completed
    frozen_edit = cli('focus', 'edit', '--project', project['id'], '--focus', original_id,
                      '--description', 'A completed Focus must remain frozen', ok=False)
    _error(frozen_edit, {'release_frozen'})
    existing_before = api('GET', '/tasks/' + tasks[0]['id'])
    frozen_assign = cli('focus', 'assign', '--project', project['id'], '--focus', original_id,
                        '--task', tasks[0]['key'], ok=False)
    assert frozen_assign['failed']['error']['code'] == 'release_frozen', frozen_assign
    assert api('GET', '/tasks/' + tasks[0]['id']) == existing_before
    invalid_reason = cli('focus', 'reopen', '--project', project['id'], '--focus', original_id,
                         '--reason', ' ', ok=False)
    _error(invalid_reason, {'client_error'})
    reopened = cli('focus', 'reopen', '--project', project['id'], '--focus', original_id,
                   '--reason', 'Synthetic follow-up scope approved')
    assert reopened['focus']['status'] == 'planned'
    assert reopened['focus']['version'] > completed['focus']['version']
    unassigned = cli('focus', 'unassign', '--project', project['id'], '--focus', original_id,
                     '--task', backlog['key'])
    removed = api('GET', '/tasks/' + backlog['id'])
    assert removed.get('release_id') is None
    assert _membership_snapshot(removed) == _membership_snapshot(done), (done, removed)
    history = api('GET', '/tasks/' + backlog['id'] + '/timeline?limit=100')
    membership_changes = [row for row in history['data'] if row['kind'] == 'task_change'
                          and row['change']['event_type'] == 'task.release_changed']
    assert len(membership_changes) == 2, membership_changes
    deleted = cli('focus', 'delete', '--project', project['id'], '--focus', original_id)
    assert deleted['deleted']
    missing = api('GET', '/releases/' + original_id, expected=404)
    _error(missing, {'release_not_found'})
    receipts.append({'name': 'explicit_completion_frozen_scope_reopen_unassign_delete',
                     'ready_status_before_explicit_complete': ready['status'], 'completed': completed,
                     'frozen_edit': frozen_edit, 'frozen_assign': frozen_assign,
                     'empty_reason': invalid_reason, 'reopened': reopened, 'unassigned': unassigned,
                     'membership_event_count': len(membership_changes), 'deleted': deleted})
    return receipts


def run_journeys(api, cli, db_path, project, focus, tasks, backlog):
    """Run additional synthetic journeys; return receipts for result.json."""
    receipts = _progress_journey(api, cli, db_path, project)
    receipts.extend(_mutation_journey(api, cli, project, tasks, backlog))
    completed = []
    for original in tasks:
        task = api('GET', '/tasks/' + original['id'])
        assert task.get('release_id') == focus['id'], task
        done = api('POST', '/tasks/' + task['id'] + '/complete',
                   {'comment': 'Original synthetic Focus acceptance verified'}, version=task['version'])
        assert done.get('completed_at'), done
        completed.append({'id': done['id'], 'key': done['key'], 'version': done['version']})
    ready = api('GET', '/releases/' + focus['id'])
    assert ready['status'] == 'planned' and ready['summary']['ready_to_release'], ready
    released = cli('focus', 'complete', '--project', project['id'], '--focus', focus['id'])
    assert released['focus']['id'] == focus['id'] and released['focus']['status'] == 'released'
    legacy_get = cli('releases', 'get', '--project', project['id'], '--release', focus['id'])
    assert legacy_get['id'] == focus['id'] and legacy_get['status'] == 'released'
    receipts.append({'name': 'original_focus_members_finish_before_explicit_completion',
                     'completed_members': completed, 'readiness': ready['summary'],
                     'status_before_explicit_completion': ready['status'], 'completed_focus': released,
                     'legacy_read_retains_id': legacy_get['id']})
    return receipts
