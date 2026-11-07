"""Conservative, versioned activity evidence from a task's durable timeline."""
from datetime import datetime, timezone

CLASSIFICATION_VERSION = 1
_PROGRESS_FIELDS = ('phase', 'checkpoint_completed', 'checkpoint_total',
                    'checkpoint_refs', 'next_action', 'attachments')
_IGNORED = {'task.claimed', 'task.claim_renewed', 'task.claim_released',
            'task.heartbeat', 'task.assigned', 'task.release_changed', 'task.released',
            'task.checklist_reordered', 'task.reordered'}


def parse_time(value):
    try:
        result = datetime.fromisoformat(str(value).replace('Z', '+00:00'))
        return result.astimezone(timezone.utc) if result.tzinfo else None
    except (TypeError, ValueError, OverflowError):
        return None


def activity_evidence(task, rows, *, complete):
    """Return last qualifying activity, a lower bound, or unknown.

    Legacy task.updated payloads do not say which fields changed. Reporting
    unknown avoids using a recent assignment/priority edit to hide old work.
    Repeated pulses are compared chronologically, never to the latest snapshot.
    """
    latest = (task.get('created_at'), 'task.created') if complete else (None, None)
    uncertain = []
    previous_progress = None
    checklist = {}
    for row in sorted(rows, key=lambda x: (parse_time(x.get('created_at')) or
                      datetime.min.replace(tzinfo=timezone.utc), str(x.get('id', '')))):
        at = row.get('created_at')
        if not parse_time(at):
            uncertain.append(at)
            continue
        kind = row.get('kind')
        source = None
        if kind == 'comment':
            actor_kind = (row.get('actor') or {}).get('kind')
            if actor_kind == 'human':
                source = 'human.comment'
            elif actor_kind != 'agent':
                uncertain.append(at)
        elif kind == 'agent_progress':
            pulse = row.get('progress') or {}
            signature = tuple(repr(pulse.get(field)) for field in _PROGRESS_FIELDS)
            if previous_progress is not None and signature != previous_progress:
                source = 'agent.progress_changed'
            elif previous_progress is None and complete:
                source = 'agent.progress_started'
            previous_progress = signature
        elif kind == 'task_change':
            change = row.get('change') or {}
            event = change.get('event_type', '')
            payload = change.get('payload') or {}
            if event in _IGNORED:
                continue
            if event == 'task.created':
                source = event
            elif event == 'task.updated':
                fields = set(payload.get('changed_fields') or [])
                if fields & {'title', 'description', 'column_id'} or payload.get('completed_at'):
                    source = 'task.scope_or_state_changed'
                elif 'changed_fields' not in payload:
                    uncertain.append(at)
            elif event == 'task.moved':
                if payload.get('from_column_id') != payload.get('to_column_id'):
                    source = event
            elif event.startswith('task.dependency_'):
                source = event
            elif event == 'task.checklist_item_updated':
                ident = payload.get('item_id', '')
                completed = payload.get('completed')
                if completed is True and checklist.get(ident) is not True:
                    source = 'task.checklist_completed'
                checklist[ident] = completed
            elif event == 'task.checklist_item_added':
                checklist[payload.get('item_id', '')] = payload.get('completed', False)
                source = 'task.checklist_scope_changed'
            elif event == 'task.checklist_item_removed':
                source = 'task.checklist_scope_changed'
            elif event in {'task.completed', 'task.blocked', 'task.reopened',
                           'bug.resolved', 'bug.reopened', 'task.resolved'}:
                source = event
            else:
                uncertain.append(at)
        else:
            uncertain.append(at)
        if source:
            latest = (at, source)
    known_at = parse_time(latest[0])
    ambiguous = any(parse_time(at) is None or known_at is None or parse_time(at) >= known_at
                    for at in uncertain)
    if known_at is None or ambiguous:
        return {'status': 'unknown', 'at': None, 'source': None,
                'reason': 'incomplete_or_ambiguous_history',
                'classification_version': CLASSIFICATION_VERSION}
    return {'status': 'known' if complete else 'lower_bound',
            'at': latest[0], 'source': latest[1],
            'classification_version': CLASSIFICATION_VERSION}
