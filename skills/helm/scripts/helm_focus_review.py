"""Bounded read-only Focus discovery and evidence-based Backlog review."""
from datetime import datetime, timezone
import hashlib
import json
from urllib.parse import quote, urlencode

from helm_config import HelmError, MAX_RELEASE_QUEUE_RESTARTS
from helm_collections import _release_project, _release_reference, _data, _collection_cursor
from helm_focus_activity import activity_evidence
from helm_focus_findings import _findings, _sort_task
from helm_focus_reader import ReviewReader, ReviewLimit, read_cursor, write_cursor
from helm_focus_signal import Signal, listing_supports, neglect_query, supported, task_supports


def _positive(args, name, default, maximum):
    value = getattr(args, name, default)
    if isinstance(value, bool) or not isinstance(value, int) or not 1 <= value <= maximum:
        raise HelmError(f'{name.replace("_", "-")} must be between 1 and {maximum}')
    return value


def _scope(task, project):
    if task.get('project_id') != project:
        raise HelmError('Focus review received a task outside its project')
    if not task.get('id') or not task.get('key'):
        raise HelmError('Focus review received an invalid task')


def cmd_focus_review(client, args):
    limits = {name: _positive(args, name, default, maximum) for name, default, maximum in (
        ('inactive_days', 14, 36500), ('focus_limit', 20, 20), ('task_limit', 200, 200),
        ('history_limit', 50, 50), ('timeline_limit', 100, 100),
        ('request_limit', 100, 500), ('time_limit', 30, 120))}
    reader = ReviewReader(client, limits['request_limit'], limits['time_limit'])
    now = datetime.now(timezone.utc)
    focus_ref = str(getattr(args, 'release', '') or '')
    result = {'evaluated_at': now.isoformat(), 'inactive_days': limits['inactive_days'],
              'focus': [], 'tasks': [], 'findings': [], 'warnings': [], 'next_cursor': '',
              'coverage': {'partial': False, 'histories_read': 0, 'limits': limits}}
    with reader.deadline():
        project = _release_project(reader, args.project)
        project_id = project['id']
        result['project'] = {field: project.get(field) for field in ('id', 'key', 'name')}
        state = read_cursor(str(getattr(args, 'cursor', '') or ''), project_id, focus_ref)
        scope = {}
        dispositions = {}
        queues_next = {}
        focus_after = state.get('focus_after', '')
        backlog_after = state.get('backlog_after', '')
        focus_next, backlog_next = focus_after, backlog_after
        offset = state.get('offset', 0)
        days = limits['inactive_days']
        # Server neglect signal (TC-381): known from a prior page, or detected
        # below at no extra request. None keeps version 1 byte-for-byte.
        signal = Signal(days) if state.get('signal') else None
        if signal and not supported(days):
            raise HelmError('This continuation used the server signal; keep --inactive-days at most 3650')
        undetermined_after = state.get('undetermined_after', '')
        undetermined_next = undetermined_after
        backlog_listed = False

        def signal_state(after):
            return {'signal': 1, 'undetermined_after': after} if signal else {}

        def neglect_page(query):
            payload, _ = reader.call('GET', f'/projects/{quote(project_id, safe="")}/tasks?' + urlencode(query))
            if not listing_supports(payload):
                raise HelmError('The server stopped reporting the neglect signal; restart the review without --cursor')
            signal.observe(payload)
            return payload

        def anchor():
            # One server instant and coverage when no Backlog listing ran.
            neglect_page(neglect_query(days, limit=1, **({'release': selected[0]['id']} if focus_ref else {})))
        fingerprint = ''
        selected = []
        columns = {}
        inventory_complete = False
        try:
            payload, _ = reader.call('GET', f'/projects/{quote(project_id, safe="")}/columns?limit=200')
            columns = {c['id']: c.get('semantic_state') for c in _data(payload)}
            if focus_ref:
                _, release = _release_reference(reader, args.project, focus_ref)
                if release.get('status') != 'planned':
                    raise HelmError('Routine review requires an active Focus; use focus get for completed history')
                selected = [release]
                focus_next = '!'
            elif focus_after != '!':
                query = {'status': 'planned', 'limit': limits['focus_limit']}
                if focus_after:
                    query['cursor'] = focus_after
                payload, _ = reader.call('GET', f'/projects/{quote(project_id, safe="")}/releases?' + urlencode(query))
                selected = _data(payload)
                focus_next = _collection_cursor(payload) or '!'
            for release in selected:
                if release.get('project_id') != project_id:
                    raise HelmError('Focus review received a Focus outside its project')
                result['focus'].append({k: release.get(k) for k in ('id', 'name', 'target_date', 'summary')})
                if release.get('target_date') and release['target_date'] < now.date().isoformat():
                    result['warnings'].append({'focus_id': release['id'], 'reason': 'focus_target_overdue'})
                cursor = state.get('queues', {}).get(release['id'], '')
                if cursor == '!':
                    continue
                for attempt in range(MAX_RELEASE_QUEUE_RESTARTS + 1):
                    query = {'limit': limits['task_limit']}
                    if cursor:
                        query['cursor'] = cursor
                    try:
                        queue, _ = reader.call('GET', '/releases/' + quote(release['id'], safe='') + '/work-queue?' + urlencode(query))
                        break
                    except HelmError as exc:
                        if exc.error_code != 'release_queue_changed' or attempt == MAX_RELEASE_QUEUE_RESTARTS:
                            raise
                        cursor = ''
                queues_next[release['id']] = _collection_cursor(queue) or '!'
                for item in _data(queue):
                    task = item.get('task') or {}
                    # Queue tasks are compact {id,key,version} references.
                    # Hydrate only the bounded history slice, then enforce
                    # project scope before using any task content.
                    if not task.get('id') or not task.get('key'):
                        raise HelmError('Focus queue returned an invalid task reference')
                    scope[task['id']] = task
                    # A task may occur as a prerequisite in several queues.
                    # Retain every disposition rather than overwrite conflicts.
                    dispositions.setdefault(task['id'], set()).add(item.get('disposition'))
            if not focus_ref and backlog_after != '!':
                query = {'state': 'backlog', 'release': 'unassigned', 'limit': limits['task_limit']}
                if supported(days):  # older servers ignore it: same request, same answer
                    query['inactive_days'] = days
                if backlog_after:
                    query['cursor'] = backlog_after
                payload, _ = reader.call('GET', f'/projects/{quote(project_id, safe="")}/tasks?' + urlencode(query))
                backlog_listed = True
                if listing_supports(payload):
                    signal = signal or Signal(days)
                    signal.observe(payload)
                elif signal:
                    raise HelmError('The server stopped reporting the neglect signal; restart the review without --cursor')
                for task in _data(payload):
                    _scope(task, project_id)
                    scope[task['id']] = task
                backlog_next = _collection_cursor(payload) or '!'
            elif focus_ref:
                backlog_next = '!'
            if signal and not focus_ref and undetermined_after != '!':
                # Bounded cards inside the window: undetermined, never active.
                query = neglect_query(days, 'undetermined', state='backlog', release='unassigned',
                                      limit=limits['task_limit'])
                if undetermined_after:
                    query['cursor'] = undetermined_after
                payload = neglect_page(query)
                for task in _data(payload):
                    _scope(task, project_id)
                    scope[task['id']] = task
                undetermined_next = _collection_cursor(payload) or '!'
            elif signal and focus_ref:
                undetermined_next = '!'
            if signal and signal.evaluated_at is None:
                anchor()
            inventory_complete = True
            tasks = sorted((t for t in scope.values() if not t.get('completed_at')
                            and columns.get(t.get('column_id')) != 'completed'),
                           key=lambda task: _sort_task(task, columns, now, dispositions.get(task['id'], ())))
            fingerprint = hashlib.sha256(json.dumps([t['id'] for t in tasks]).encode()).hexdigest()
            if offset and state.get('fingerprint') != fingerprint:
                raise HelmError('Focus review inventory changed; restart without --cursor')
            for task in tasks[offset:offset + limits['history_limit']]:
                if 'project_id' not in task:
                    task, _ = reader.call('GET', '/tasks/' + quote(task['id'], safe=''))
                    _scope(task, project_id)
                if task.get('completed_at') or columns.get(task.get('column_id')) == 'completed':
                    offset += 1
                    continue
                if signal is None and supported(days) and not backlog_listed and task_supports(task):
                    signal = Signal(days)
                    anchor()
                activity = {'status': 'unknown', 'at': None, 'source': None, 'reason': 'history_unavailable'}
                timeline_cursor = ''
                if signal:
                    activity = signal.assess(task)  # no timeline read on the server path
                else:
                    try:
                        payload, _ = reader.call('GET', '/tasks/' + quote(task['id'], safe='') + '/timeline?' +
                                                 urlencode({'limit': limits['timeline_limit']}))
                        timeline_cursor = _collection_cursor(payload)
                        activity = activity_evidence(task, _data(payload), complete=not timeline_cursor)
                        result['coverage']['histories_read'] += 1
                    except ReviewLimit:
                        raise
                    except HelmError as exc:
                        result['warnings'].append({'task_key': task['key'], 'reason': 'history_unavailable',
                                                   'status': exc.status_code})
                task_dispositions = dispositions.get(task['id'], set())
                result['tasks'].append({'id': task['id'], 'key': task['key'], 'title': task.get('title'),
                                        'activity': activity, 'timeline_next': timeline_cursor,
                                        'dispositions': sorted(d for d in task_dispositions if d)})
                if timeline_cursor:
                    result.setdefault('history_continuations', []).append({
                        'task_key': task['key'], 'before': timeline_cursor,
                        'next_action': 'Read older evidence with timeline --task TASK_KEY --before CURSOR; capped review cannot conclude inactivity.'})
                result['findings'].extend(_findings(task, activity, now, limits['inactive_days'],
                                                    columns.get(task.get('column_id')), None))
                for disposition in sorted(d for d in task_dispositions if d):
                    if disposition != 'completed':
                        result['findings'].extend(row for row in _findings(
                            task, {'status': 'known', 'at': now.isoformat()}, now,
                            limits['inactive_days'], 'ready', disposition) if row['reason'] == disposition)
                offset += 1
            if offset < len(tasks):
                result['next_cursor'] = write_cursor(project_id, focus_ref, offset=offset,
                    fingerprint=fingerprint, focus_after=focus_after, backlog_after=backlog_after,
                    queues=state.get('queues', {}), **signal_state(undetermined_after))
            elif any(value != '!' for value in queues_next.values()):
                result['next_cursor'] = write_cursor(project_id, focus_ref, offset=0,
                    focus_after=focus_after, backlog_after=backlog_after, queues=queues_next,
                    **signal_state(undetermined_after))
            elif focus_next != '!' or backlog_next != '!' or (signal and undetermined_next != '!'):
                result['next_cursor'] = write_cursor(project_id, focus_ref, offset=0,
                    focus_after=focus_next, backlog_after=backlog_next, **signal_state(undetermined_next))
        except ReviewLimit:
            result['warnings'].append({'reason': 'request_or_time_budget_exhausted'})
            if inventory_complete:
                result['next_cursor'] = write_cursor(project_id, focus_ref, offset=offset,
                    fingerprint=fingerprint, focus_after=focus_after, backlog_after=backlog_after,
                    queues=state.get('queues', {}), **signal_state(undetermined_after))
            else:
                result['resume'] = {'restart_required': True,
                    'next_action': 'Increase --request-limit/--time-limit or narrow --focus; inventory could not finish within this budget.'}
        result['coverage'].update(requests=reader.requests, candidates=len(scope),
                                  inventory_complete=inventory_complete,
                                  ordering='owned queue work, available due/blocker fields, stable task key; findings order applies to inspected evidence',
                                  histories_unavailable=sum(
                                      t['activity'].get('assessment') in ('unknown', 'pending') if signal
                                      else t['activity']['status'] != 'known' for t in result['tasks']))
        result['coverage']['partial'] = bool(result['next_cursor'] or
            result['coverage']['histories_unavailable'] or not inventory_complete)
        if signal:
            result['activity_signal'] = signal.summary()
            if signal.partial:
                result['warnings'].append({'reason': 'activity_coverage_incomplete', **signal.coverage,
                    'next_action': 'Rerun once the server catches up; incomplete coverage is not evidence that nothing is neglected.'})
                result['coverage']['partial'] = True
        order = {'overdue': 0, 'blocked': 1, 'cross_release_conflict': 1,
                 'manually_blocked': 1, 'dependency_blocked': 1, 'inactive': 2}
        result['findings'].sort(key=lambda item: (order.get(item['reason'], 3), item['task_key'], item['reason']))
    return result
