"""Focus review ordering and evidence findings for both classification paths."""
from helm_focus_activity import parse_time
from helm_focus_signal import activity_rows


def _sort_task(task, columns, now, dispositions=()):
    due = parse_time(task.get('due_at'))
    blocked = (task.get('dependency_summary') or {}).get('blocked') or columns.get(task.get('column_id')) == 'blocked'
    return (0 if 'owned' in dispositions else 1,
            0 if due and due <= now else 1,
            0 if blocked or set(dispositions) & {'dependency_blocked', 'manually_blocked', 'cross_release_conflict'} else 1,
            task.get('key', ''))


def _findings(task, activity, now, days, semantic, disposition):
    rows = []
    def add(reason, next_action, timestamp=None, source=None):
        rows.append({'task_id': task['id'], 'task_key': task['key'],
                     'focus_id': task.get('release_id'), 'reason': reason,
                     'at': timestamp, 'source': source, 'next_action': next_action})
    due = parse_time(task.get('due_at'))
    if due and due <= now:
        add('overdue', 'Review the due work and agree a concrete next action.', task['due_at'], 'task.due_at')
    if semantic == 'blocked' or (task.get('dependency_summary') or {}).get('blocked'):
        add('blocked', 'Inspect unresolved prerequisites or the recorded manual blocker.')
    if disposition in {'owned', 'claimed_elsewhere', 'cross_release_conflict', 'manually_blocked', 'dependency_blocked'}:
        actions = {'owned': 'Resume owned work before claiming another card.',
                   'claimed_elsewhere': 'Coordinate with the current owner; do not override the claim.',
                   'cross_release_conflict': 'Review the prerequisite assigned to another active Focus.',
                   'manually_blocked': 'Resolve the recorded manual blocker within authorized scope.',
                   'dependency_blocked': 'Review the prerequisite before claiming dependent work.'}
        add(disposition, actions[disposition])
    at = parse_time(activity.get('at'))
    if 'assessment' in activity:  # server signal: the assessment already applied the threshold
        for reason, action, timestamp, source in activity_rows(activity):
            add(reason, action, timestamp, source)
    elif activity['status'] == 'known' and at and (now - at).total_seconds() >= days * 86400:
        add('inactive', 'Choose resume, clarify, defer with a review trigger, or close within authorized scope.',
            activity['at'], activity['source'])
    elif activity['status'] != 'known':
        add('activity_unknown', 'Inspect the missing or ambiguous history before drawing an inactivity conclusion.')
    if not task.get('assignee') and not task.get('claimed_by'):
        add('unowned', 'Identify who should take the next action; do not auto-assign.')
    if not (task.get('agent_work') or {}).get('next_action'):
        add('next_action_missing', 'Clarify the next step; absence of structured progress is not a failure.')
    description = (task.get('description') or '').casefold()
    if not (task.get('checklist_summary') or {}).get('total') and not any(
            word in description for word in ('acceptance', 'checkpoint', 'finish condition')):
        add('acceptance_evidence_missing', 'Read the card and clarify its finish condition if needed.')
    return rows
