"""Writer coverage, poison rows, deletion and scope: plan cases 5, 6, 7, 10, 11, 13.

Case 6 owns a private server that is stopped and restarted with another
binary; the others share the runner's server. Direct SQL appears only where a
real writer cannot produce the row (poison history) and is called out there.
Checks named store_* observe the projector's stored rows (TC-379, landed) and
must pass; the API checks stay FeatureAbsent until TC-380.
"""
import uuid

from contract import Checks, activity, ids, neglected, stored_activity, table_rows, task_activity
from harness import Server, World, parse


def _advanced(clock, value):
    assert value['status'] in ('known', 'bounded'), value
    at = parse(value['not_after'])
    assert at is not None and at >= clock.t0, ('signal did not advance', value)
    return value


def _excluded(clock, value):
    assert value['status'] == 'known' and parse(value['not_after']) == parse(clock.ago(30)), value
    return value


def case_05_every_writer(ctx, receipt):
    w, clock = ctx.world, ctx.clock
    dependent, prerequisite = w.old_task('Synthetic dependent', 30), w.old_task('Synthetic prerequisite', 30)
    parent, child = w.old_task('Synthetic parent', 30), w.old_task('Synthetic child', 30)
    w.depend(dependent, prerequisite)
    w.link_child(parent, child)
    carriers = {}
    for name, task in (('parent', parent), ('child', child)):
        if any((r.get('change') or {}).get('event_type', '').startswith('task.parent_')
               for r in w.timeline(task)):
            carriers[name] = task
    imported = import_copy(w, portable_source(w))
    receipt['fixture'] = {'history_at': 'T0-30d', 'hierarchy_event_on': sorted(carriers),
                          'imported_tasks': len(imported)}
    checks = Checks(receipt)
    checks.check('store_dependency_writer_advances_row', lambda: _advanced(clock, stored_activity(w.s, dependent['id'])))

    # Plan D2 (TC-378) makes task.parent_linked excluded, so the hierarchy
    # writer must be processed (row recomputed after its event) without
    # refreshing the card: not_after stays at the T0-30d history.
    def hierarchy_processed_not_refreshed(task, value):
        assert value['status'] == 'known' and parse(value['not_after']) == parse(clock.ago(30)), value
        computed, linked = table_rows(w.s, "SELECT s.computed_at, (SELECT MAX(e.created_at) FROM events e WHERE "
                                           "e.task_id=s.task_id AND e.type LIKE 'task.parent_%') FROM "
                                           "task_activity_signal s WHERE s.task_id=?", (task['id'],))[0]
        assert linked and parse(computed) >= parse(linked), ('row not rebuilt after the link', computed, linked)
        return value
    for name, task in carriers.items():
        checks.check(f'store_hierarchy_writer_processed_{name}', lambda task=task: hierarchy_processed_not_refreshed(
            task, stored_activity(w.s, task['id'])))
    checks.check('store_portable_import_writer_produces_rows',
                 lambda: {t['key']: stored_activity(w.s, t['id'])['status'] for t in imported})
    checks.check('dependency_writer_advances_signal', lambda: _advanced(clock, task_activity(w, dependent)))
    for name, task in carriers.items():
        checks.check(f'hierarchy_link_excluded_{name}', lambda task=task: _excluded(clock, task_activity(w, task)))

    def hierarchy_event_exists():
        assert carriers, 'no task.parent_* event found on either task'
        return sorted(carriers)
    checks.check('hierarchy_event_exists', hierarchy_event_exists)

    def import_rows():
        rows = {t['key']: activity(w.s.api('GET', '/tasks/' + t['id']))['status'] for t in imported}
        assert rows and 'pending' not in rows.values(), rows
        return rows
    checks.check('portable_import_writer_produces_rows', import_rows)
    checks.finish()


def case_07_import_older_history(ctx, receipt):
    w, clock = ctx.world, ctx.clock
    source = portable_source(w)
    imported = import_copy(w, source)
    by_title = {t['title']: t for t in imported}
    receipt['fixture'] = {'source_history': 'T0-90d created, T0-45d human comment', 'imported': len(imported)}
    checks = Checks(receipt)

    def same_as_rebuild(read):
        original, copy = read(source), read(by_title[source['title']])
        keys = ('status', 'last_meaningful_at', 'source', 'not_after', 'reason')
        assert {k: original[k] for k in keys} == {k: copy[k] for k in keys}, (original, copy)
        assert parse(original['not_after']) == parse(clock.ago(45)), original
        return copy
    checks.check('store_imported_row_equals_source_rebuild',
                 lambda: same_as_rebuild(lambda task: stored_activity(w.s, task['id'])))
    checks.check('imported_row_equals_source_rebuild', lambda: same_as_rebuild(
        lambda task: task_activity(w, task) if task is source else activity(w.s.api('GET', '/tasks/' + task['id']))))
    checks.finish()


def portable_source(w):
    """One card whose history is old: created T0-90d, human comment T0-45d."""
    if getattr(w, '_portable_source', None):
        return w._portable_source
    task = w.task('Synthetic portable history card')
    comment = w.comment(task, 'Synthetic imported evidence')
    w.backdate(task, w.clock.ago(90))
    with w.s.db() as db:
        db.execute('UPDATE comments SET created_at=?, updated_at=? WHERE id=?',
                   (w.clock.ago(45), w.clock.ago(45), comment['id']))
        db.execute("UPDATE events SET created_at=? WHERE task_id=? AND type='comment.created'",
                   (w.clock.ago(45), task['id']))
    w.touch(task)
    w._portable_source = task
    return task


def import_copy(w, source):
    """Export the project and import it as a new project; returns imported tasks."""
    if getattr(w, '_imported', None) is not None:
        return w._imported
    archive = w.s.api('GET', f'/projects/{w.key}/export')
    key = 'IMP' + uuid.uuid4().hex[:4].upper()
    w.s.api('POST', '/projects', {'key': key, 'name': 'Synthetic import target'}, expected=201)
    w.s.api('POST', f'/projects/{key}/import', {'archive': archive, 'conflict': 'remap'})
    tasks, cursor = [], None
    while True:
        page = w.s.api('GET', f'/projects/{key}/tasks?limit=200' + (f'&cursor={cursor}' if cursor else ''))
        tasks.extend(page['data'])
        cursor = page.get('next_cursor')
        if not cursor:
            break
    assert any(t['title'] == source['title'] for t in tasks), 'import lost the source card'
    w._imported = tasks
    return tasks


def case_06_rollback_binary(ctx, receipt):
    clock = ctx.clock
    server = Server(ctx.binary, ctx.workdir, name='rollback')
    server.start()
    try:
        w = World(server, clock, key='RBK')
        task = w.old_task('Synthetic rollback card', 30)
        server.stop()
        # Same Server object and database file, now served by the retained binary.
        server.start(binary=ctx.rollback_binary)
        w.comment(task, 'Synthetic comment written by the retained binary')
        old_read = w.get(task)
        old_list = server.api('GET', '/projects/RBK/tasks?limit=50')
        server.stop()
        server.start(binary=ctx.binary)
        receipt['fixture'] = {'history_at': 'T0-30d', 'retained_binary_sha256': ctx.rollback_sha,
                              'retained_binary_is_candidate': ctx.rollback_binary == ctx.binary,
                              'retained_read_ok': old_read['id'] == task['id'],
                              'retained_list_count': len(old_list['data'])}
        checks = Checks(receipt)

        def reconciled(value):
            assert value['status'] == 'known' and value['source'] == 'human.comment', value
            return _advanced(clock, value)
        checks.check('store_tail_reconciles_retained_binary_events',
                     lambda: reconciled(stored_activity(server, task['id'])))
        checks.check('tail_reconciles_retained_binary_events', lambda: reconciled(task_activity(w, task)))
        checks.check('restarted_listing_complete', lambda: neglected(server, 'RBK', 14)[1])
        checks.finish()
    finally:
        server.stop()


def case_10_poison_history(ctx, receipt):
    w, clock = ctx.world, ctx.clock
    poison = {name: w.old_task(f'Synthetic poison {name}', 30)
              for name in ('malformed_payload', 'unparseable_time', 'unknown_type')}
    healthy = w.old_task('Synthetic healthy neighbour', 30)
    rows = {'malformed_payload': ('task.updated', '{"changed_fields": ["title"', clock.ago(5)),
            'unparseable_time': ('task.updated', '{"changed_fields": ["title"]}', 'not-a-timestamp'),
            'unknown_type': ('task.synthetic_future_kind', '{}', clock.ago(5))}
    # No server writer can emit these rows, so they are inserted at new
    # cursors exactly as a corrupted or future binary would leave them.
    with w.s.db() as db:
        for name, (kind, payload, at) in rows.items():
            db.execute('INSERT INTO events(id,type,actor_id,project_id,task_id,payload,created_at) '
                       'VALUES (?,?,?,?,?,?,?)', (uuid.uuid4().hex, kind, 'actor-disabled-mode',
                                                  w.project['id'], poison[name]['id'], payload, at))
    w.comment(healthy, 'Synthetic later comment')
    timeline_status = {name: w.s.call('GET', f'/tasks/{task["id"]}/timeline?limit=100')[0]
                       for name, task in poison.items()}
    receipt['fixture'] = {'poison_rows': {k: [v[0], 'malformed' if k == 'malformed_payload' else 'valid',
                                              clock.label(v[2])] for k, v in rows.items()},
                          'existing_timeline_status': timeline_status}
    checks = Checks(receipt)

    def recorded(read, name, statuses, reasons, not_after=None):
        value = read(poison[name])
        assert value['status'] in statuses and value['reason'] in reasons, value
        if not_after:
            assert parse(value['not_after']) == parse(not_after), value
        return value
    for prefix, read in (('store_', lambda task: stored_activity(w.s, task['id'])),
                         ('', lambda task: task_activity(w, task))):
        checks.check(prefix + 'malformed_payload_recorded', lambda read=read: recorded(
            read, 'malformed_payload', {'unknown', 'bounded'},
            {'classification_error', 'legacy_update', 'unknown_event_type'}))
        checks.check(prefix + 'unparseable_time_recorded', lambda read=read: recorded(
            read, 'unparseable_time', {'unknown'}, {'unparseable_time'}))
        checks.check(prefix + 'unknown_type_bounded', lambda read=read: recorded(
            read, 'unknown_type', {'bounded'}, {'unknown_event_type'}, clock.ago(5)))
        checks.check(prefix + 'projector_not_stalled', lambda read=read: _advanced(clock, read(healthy)))
    checks.finish()


def case_11_deletion_and_restore(ctx, receipt):
    w = ctx.world
    task = w.old_task('Synthetic deleted card', 30)
    w.s.api('DELETE', '/tasks/' + task['id'], version=w.version(task), expected=204)
    checks = Checks(receipt)

    def soft_deleted_excluded():
        assert task['id'] not in ids(neglected(w.s, w.key, 14)[0]), 'soft-deleted card listed'
        return True
    checks.check('soft_deleted_excluded', soft_deleted_excluded)
    with w.s.db() as db:
        version = db.execute('SELECT version FROM tasks WHERE id=?', (task['id'],)).fetchone()[0]
    restored = w.s.api('POST', f'/tasks/{task["id"]}/restore', {}, version=version)
    receipt['fixture'] = {'history_at': 'T0-30d', 'restored_version': restored['version'],
                          'hard_delete': 'no API path; Go test TestTaskActivitySignalHardDeleteAndRestore'}

    def stored_reclassified():
        value = stored_activity(w.s, task['id'])
        computed = table_rows(w.s, 'SELECT computed_at FROM task_activity_signal WHERE task_id=?', (task['id'],))[0]
        assert parse(computed[0]) >= parse(restored['updated_at']), computed
        assert value['source'] == 'task.restored' and parse(value['not_after']) >= w.clock.t0, value
        return value
    checks.check('store_restored_card_reclassified', stored_reclassified)
    checks.check('restored_card_reclassified', lambda: task_activity(w, task))
    checks.finish()


def case_13_scope_and_permissions(ctx, receipt):
    w = ctx.world
    foreign = World(w.s, ctx.clock, key='FRN' + uuid.uuid4().hex[:3].upper())
    foreign_task = foreign.old_task('Synthetic foreign card', 30)
    w.old_task('Synthetic scoped card', 30)
    reader = w.s.api('POST', f'/agents/{w.agent_id}/tokens',
                     {'name': 'synthetic-membership-only', 'scopes': ['projects:read'],
                      'project_ids': [w.project['id']]}, expected=201)['token']
    receipt['fixture'] = {'token_scopes': 'project-scoped agent with tasks:read; second token projects:read only',
                          'foreign_project': 'second synthetic project outside the token ceiling'}
    checks = Checks(receipt)

    def subset_of_plain():
        plain = w.s.api('GET', f'/projects/{w.key}/tasks?limit=200', token=w.token)['data']
        tasks, _ = neglected(w.s, w.key, 14, token=w.token, limit=200)
        assert set(ids(tasks)) <= set(ids(plain)), 'neglect listing exceeded the plain listing'
        assert all(activity(t) for t in tasks)
        return len(tasks)

    def foreign_denied_identically():
        plain = w.s.call('GET', f'/projects/{foreign.key}/tasks', token=w.token)
        status, body = w.s.call('GET', f'/projects/{foreign.key}/tasks?inactive_days=14', token=w.token)
        assert (status, (body or {}).get('error', {}).get('code')) == \
            (plain[0], (plain[1] or {}).get('error', {}).get('code')), (plain, status, body)
        assert status in (403, 404), status
        return status

    def foreign_task_hidden():
        status, body = w.s.call('GET', '/tasks/' + foreign_task['id'], token=w.token)
        assert status in (403, 404) and 'activity' not in (body or {}), (status, body)
        return status

    def compact_has_no_activity():
        status, body = w.s.call('GET', f'/projects/{w.key}/tasks?inactive_days=14', token=reader)
        assert status in (403, 404) or all('activity' not in t for t in body.get('data', [])), body
        return status

    checks.check('scoped_token_listing_is_subset', subset_of_plain)
    checks.check('foreign_project_denied_like_plain_listing', foreign_denied_identically)
    checks.check('foreign_task_activity_not_leaked', foreign_task_hidden)
    checks.check('no_tasks_read_scope_sees_no_activity', compact_has_no_activity)
    checks.finish()


CASES = {5: case_05_every_writer, 6: case_06_rollback_binary, 7: case_07_import_older_history,
         10: case_10_poison_history, 11: case_11_deletion_and_restore, 13: case_13_scope_and_permissions}
