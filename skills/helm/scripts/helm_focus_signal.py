"""Server neglect signal (classification version 2) for focus review.

A server that has the signal answers neglect listings with `evaluated_at` and
`activity_coverage`, and every task read carries `activity`. Older servers
ignore the listing inputs, so detection costs no extra request and the
version 1 path stays byte-for-byte unchanged. One server instant per
invocation governs every threshold comparison, exactly as the server's own
`inactive_days` rule: inactive when `not_after <= evaluated_at - N days`.
"""
from datetime import timedelta

from helm_focus_activity import parse_time

SERVER_MAX_DAYS = 3650
CLASSIFICATION_VERSION = 2
UNUSED_LIMITS = ['timeline_limit']
NEXT_ACTIONS = {
    'undetermined': 'Activity is only bounded by not_after, inside the window: it may be recent or old. '
                    'Inspect the timeline before concluding; never describe it as active.',
    'pending': 'The server has not processed this card\'s newest events. Rerun the review before '
               'concluding anything about its activity.',
}


def supported(days):
    """Thresholds beyond the server maximum keep version 1 classification."""
    return days <= SERVER_MAX_DAYS


def listing_supports(payload):
    return isinstance(payload, dict) and 'evaluated_at' in payload and 'activity_coverage' in payload


def task_supports(task):
    return isinstance(task, dict) and isinstance(task.get('activity'), dict)


def neglect_query(days, status=None, **query):
    query['inactive_days'] = days
    if status:
        query['activity_status'] = status
    return query


class Signal:
    def __init__(self, days):
        self.days = days
        self.evaluated_at = None
        self.listings = 0
        self.coverage = {'complete': True, 'lag': 0, 'pending': 0}

    def observe(self, payload):
        """Record one neglect listing page; the first instant governs the run."""
        self.listings += 1
        if self.evaluated_at is None:
            self.evaluated_at = payload.get('evaluated_at')
        page = payload.get('activity_coverage') or {}
        self.coverage['complete'] = self.coverage['complete'] and page.get('complete') is True
        self.coverage['lag'] = max(self.coverage['lag'], int(page.get('lag') or 0))
        self.coverage['pending'] += int(page.get('pending') or 0)

    @property
    def partial(self):
        return not self.coverage['complete'] or self.coverage['pending'] > 0

    def assess(self, task):
        value = task.get('activity') or {}
        status = value.get('status')
        not_after = value.get('not_after')
        evaluated = parse_time(self.evaluated_at)
        bound = parse_time(not_after)
        if status == 'pending' or evaluated is None:
            assessment = 'pending'
        elif status not in ('known', 'bounded') or bound is None:
            assessment = 'unknown'
        elif bound <= evaluated - timedelta(days=self.days):
            assessment = 'inactive'
        else:
            assessment = 'active' if status == 'known' else 'undetermined'
        return {'status': status, 'at': value.get('last_meaningful_at'), 'source': value.get('source') or None,
                'not_after': not_after, 'reason': value.get('reason') or None,
                'classification_version': value.get('classification_version'), 'assessment': assessment}

    def summary(self):
        return {'path': 'server', 'classification_version': CLASSIFICATION_VERSION,
                'evaluated_at': self.evaluated_at, 'inactive_days': self.days,
                'activity_coverage': dict(self.coverage), 'listings': self.listings,
                'backlog_scope': 'inactive, unknown and undetermined unfocused Backlog cards',
                'unused_limits': list(UNUSED_LIMITS)}


def activity_rows(activity):
    """(reason, next_action, at, source) for one server-path assessment."""
    assessment = activity['assessment']
    if assessment == 'inactive':
        return [('inactive', 'Choose resume, clarify, defer with a review trigger, or close within authorized scope.',
                 activity['not_after'], activity['source'])]
    if assessment == 'unknown':
        return [('activity_unknown', 'Inspect the missing or ambiguous history before drawing an inactivity conclusion.',
                 None, None)]
    if assessment in NEXT_ACTIONS:
        return [('activity_' + assessment, NEXT_ACTIONS[assessment], activity['not_after'], None)]
    return []
