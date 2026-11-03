"""Explicit reviewed context changes; never provision a native host binding."""
from .filesystem import BootstrapError, digest, lower_hex

ACTION = 'host-rebind'
PLAN_KEYS = {'expected_previous_host_context_sha256', 'expected_host_context_sha256',
             'expected_history_sha256'}


def validate_plan(plan):
    if (any(not lower_hex(plan[key], 64) for key in PLAN_KEYS)
            or plan['expected_previous_host_context_sha256'] == plan['expected_host_context_sha256']):
        raise BootstrapError('distinct reviewed prior and current host contexts required')


def effective_context(origin, records):
    # Call only after the complete history has been validated. A durable pending
    # rebind advances context but cannot authorize activation of its old marker.
    return records[-1]['host_context_sha256'] if records else origin['host_context_sha256']


def check(plan, origin, records, context, marker):
    previous = effective_context(origin, records)
    if (plan['expected_previous_host_context_sha256'] != previous
            or plan['expected_host_context_sha256'] != context
            or previous == context
            or plan['expected_history_sha256'] != digest(records[-1] if records else origin)
            or plan['role'] != marker['role']):
        raise BootstrapError('reviewed host rebind context, history tip or selected role differs')


def advance(record, previous):
    """Validate a context edge while folding immutable mixed-version history."""
    context = record['host_context_sha256']
    if record['action'] == ACTION:
        if (record['previous_host_context_sha256'] != previous
                or not lower_hex(context, 64) or context == previous):
            raise BootstrapError('invalid durable host rebind context edge')
    elif context != previous:
        raise BootstrapError('durable transition changed host context without reviewed rebind')
    return context
