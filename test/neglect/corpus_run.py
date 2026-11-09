#!/usr/bin/env python3
"""Run the version 1 classifier over the shared timeline corpus (plan D3, case 15).

  python3 test/neglect/corpus_run.py                 # check corpus + reference
  python3 test/neglect/corpus_run.py --record        # rewrite reference_v1.json
  python3 test/neglect/corpus_run.py --v2-results F  # also check a v2 result map

Checks, in order: every fixture is well formed; recomputed v1 results equal
the recorded reference (so v1 drift is visible); every hand-set expected_v2
satisfies the parity contract against v1 read through the plan D2 table; the
corpus covers every qualifying, excluded and ambiguous kind v1 knows and every
table row. With --v2-results (a JSON object of fixture id -> activity, as the
Go classifier emits) each result must equal expected_v2 and satisfy the same
contract. Exit status 1 on any violation.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import sys

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
V1_PATH = ROOT / 'skills/helm/scripts/helm_focus_activity.py'
sys.path.insert(0, str(V1_PATH.parent))
import helm_focus_activity as v1  # noqa: E402

CORPUS = [HERE / 'corpus' / f'{name}.json'
          for name in ('qualifying', 'excluded', 'ambiguous', 'edges', 'reclassified')]
REFERENCE = HERE / 'corpus' / 'reference_v1.json'
# Plan D2 table: the only event types version 2 classifies differently from v1.
# Parity is exact everywhere else (PARITY.md). Corpus and table change together.
TABLE_EXCLUDED = frozenset({'comment.updated', 'comment.deleted', 'task.parent_linked',
                            'task.parent_unlinked', 'task.deleted', 'attachment.updated',
                            'attachment.deleted', 'task.dependency_state_changed'})
TABLE_QUALIFYING = frozenset({'task.restored', 'bug.triaged', 'attachment.uploaded'})
# The only table row allowed to change a v1 known result.
KNOWN_CHANGE_ALLOWED = frozenset({'task.dependency_state_changed'})
V2_FIELDS = ('status', 'last_meaningful_at', 'source', 'not_after', 'reason')
REASONS = {'legacy_update', 'unknown_event_type', 'actor_unavailable', 'unparseable_time',
           'classification_error'}
REQUIRED = (
    {f'excluded:{event}' for event in v1._IGNORED}
    | {'excluded:agent.comment', 'excluded:identical_progress', 'excluded:task.moved:same_column',
       'excluded:task.updated:metadata_only', 'excluded:task.updated:empty_changed_fields',
       'excluded:task.checklist_item_updated:uncomplete',
       'excluded:task.checklist_item_updated:already_complete'}
    | {f'qualifying:{name}' for name in (
        'task.created_at', 'task.created', 'human.comment', 'agent.progress_started',
        'agent.progress_changed', 'task.updated:title', 'task.updated:description',
        'task.updated:column_id', 'task.updated:completed_at', 'task.moved', 'task.dependency_added',
        'task.dependency_removed', 'task.dependency_state_changed', 'task.checklist_completed',
        'task.checklist_item_added', 'task.checklist_item_removed', 'task.completed', 'task.blocked',
        'task.reopened', 'bug.resolved', 'bug.reopened', 'task.resolved')
       if name != 'task.dependency_state_changed'}
    | {f'reclassified:excluded:{event}' for event in TABLE_EXCLUDED}
    | {f'reclassified:qualifying:{event}' for event in TABLE_QUALIFYING}
    | {'ambiguous:task.updated:legacy', 'ambiguous:comment:actor_null',
       'ambiguous:comment:actor_unrecognized', 'ambiguous:row_kind:unknown', 'ambiguous:time:garbage',
       'ambiguous:time:naive', 'ambiguous:time:null', 'ambiguous:task_created_at:unparseable'}
    | {f'ambiguous:unlisted:{event}' for event in (
        'comment.created', 'bug.updated', 'task.progressed', 'task.synthetic_future_kind')})


def instant(value):
    return v1.parse_time(value) if value is not None else None


def load():
    fixtures = []
    for path in CORPUS:
        body = json.loads(path.read_text())
        assert body['corpus'] == 'helm.neglect.timeline' and body['complete_history'] is True, path
        fixtures.extend(body['fixtures'])
    seen = set()
    for fixture in fixtures:
        assert fixture['id'] not in seen, ('duplicate fixture id', fixture['id'])
        seen.add(fixture['id'])
        assert set(fixture) == {'id', 'covers', 'note', 'task', 'rows', 'expected_v2'}, fixture['id']
        assert set(fixture['expected_v2']) == set(V2_FIELDS), fixture['id']
    return fixtures


def run_v1(fixture):
    return v1.activity_evidence(fixture['task'], fixture['rows'], complete=True)


def event_type(row):
    return (row.get('change') or {}).get('event_type', '') if row.get('kind') == 'task_change' else ''


def run_v1_table(fixture):
    """v1 with only the plan D2 table applied: the parity baseline for v2.

    Table-excluded events with a parseable time are dropped (v1 checks time
    first, so an unparseable one stays and still makes the result unknown).
    Table-qualifying events are replayed as task.completed, a v1 lifecycle
    source, and the winning row's own event name is restored afterwards.
    """
    rows, renamed = [], {}
    for row in fixture['rows']:
        event, parsed = event_type(row), v1.parse_time(row.get('created_at'))
        if event in TABLE_EXCLUDED and parsed:
            continue
        if event in TABLE_QUALIFYING and parsed:
            row = json.loads(json.dumps(row))
            row['change']['event_type'] = 'task.completed'
            renamed[str(row.get('id', ''))] = event
        rows.append(row)
    result = v1.activity_evidence(fixture['task'], rows, complete=True)
    if result['status'] == 'known' and result['source'] == 'task.completed':
        at = instant(result['at'])
        winners = sorted(str(r.get('id', '')) for r in rows
                         if event_type(r) == 'task.completed' and instant(r.get('created_at')) == at)
        result = dict(result, source=renamed.get(winners[-1], 'task.completed'))
    return result


def contract(fixture, reference, v2, baseline=None):
    """Plan D3 parity plus D2 shape. Returns a list of violations.

    reference is the recorded v1 result; baseline is v1 read through the D2
    table (run_v1_table), which rules 1 and 2 apply to.
    """
    problems = []
    baseline = baseline or run_v1_table(fixture)
    status, row_times = v2['status'], {instant(r.get('created_at')) for r in fixture['rows']}
    row_times.add(instant(fixture['task']['created_at']))
    if reference['status'] == 'known' and baseline != reference and \
            not KNOWN_CHANGE_ALLOWED & {event_type(r) for r in fixture['rows']}:
        problems.append('the D2 table changed a v1 known result without task.dependency_state_changed')
    reference = baseline
    if reference['status'] == 'known':
        if status != 'known' or v2['source'] != reference['source'] or \
                instant(v2['last_meaningful_at']) != instant(reference['at']):
            problems.append('v1 known must match exactly (status, source, last_meaningful_at)')
    elif status not in ('bounded', 'unknown'):
        problems.append(f'v1 {reference["status"]} must map to bounded or unknown, not {status}')
    if status == 'known':
        if instant(v2['not_after']) != instant(v2['last_meaningful_at']) or v2['reason']:
            problems.append('known: not_after must equal last_meaningful_at and reason must be empty')
    elif status == 'bounded':
        if v2['last_meaningful_at'] or v2['source'] or v2['reason'] not in REASONS:
            problems.append('bounded: no last_meaningful_at/source; reason from the D2 set')
        if instant(v2['not_after']) is None or instant(v2['not_after']) not in row_times:
            problems.append('bounded: not_after must be the time of a row in the history')
    elif status == 'unknown':
        if v2['not_after'] is not None or v2['reason'] not in REASONS:
            problems.append('unknown: not_after null and reason from the D2 set')
    else:
        problems.append(f'invalid status {status}')
    return problems


def same(expected, actual):
    for field in V2_FIELDS:
        a, b = expected[field], actual.get(field)
        if field in ('last_meaningful_at', 'not_after'):
            if instant(a) != instant(b) or (a is None) != (b is None):
                return False
        elif (a or None) != (b or None):
            return False
    return True


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--record', action='store_true', help='rewrite reference_v1.json from v1')
    parser.add_argument('--v2-results', type=Path, help='JSON map fixture id -> v2 activity')
    parser.add_argument('--receipt', type=Path, help='write a JSON summary here')
    args = parser.parse_args(argv)
    fixtures = load()
    results = {f['id']: run_v1(f) for f in fixtures}
    baselines = {f['id']: run_v1_table(f) for f in fixtures}
    v1_sha = hashlib.sha256(V1_PATH.read_bytes()).hexdigest()
    if args.record:
        REFERENCE.write_text(json.dumps({'classifier': 'skills/helm/scripts/helm_focus_activity.py',
                                         'classification_version': v1.CLASSIFICATION_VERSION,
                                         'classifier_sha256': v1_sha, 'results': results},
                                        indent=1, sort_keys=True) + '\n')
    reference = json.loads(REFERENCE.read_text())
    violations = []
    if reference['results'] != results:
        drift = sorted(k for k in results if reference['results'].get(k) != results[k])
        violations.append({'check': 'v1_reference_drift', 'fixtures': drift or ['fixture set changed']})
    for fixture in fixtures:
        for problem in contract(fixture, results[fixture['id']], fixture['expected_v2'], baselines[fixture['id']]):
            violations.append({'check': 'expected_v2_contract', 'fixture': fixture['id'], 'problem': problem})
    covered = set().union(*(set(f['covers']) for f in fixtures))
    missing = sorted(REQUIRED - covered)
    if missing:
        violations.append({'check': 'coverage', 'missing': missing})
    v2_summary = None
    if args.v2_results:
        actual = json.loads(args.v2_results.read_text())
        v2_summary = {'compared': 0, 'mismatched': []}
        for fixture in fixtures:
            got = actual.get(fixture['id'])
            v2_summary['compared'] += 1
            if got is None or not same(fixture['expected_v2'], got) or \
                    contract(fixture, results[fixture['id']], got, baselines[fixture['id']]):
                v2_summary['mismatched'].append(fixture['id'])
        if v2_summary['mismatched']:
            violations.append({'check': 'v2_results', 'fixtures': v2_summary['mismatched']})
    counts = {}
    for fixture in fixtures:
        key = (results[fixture['id']]['status'], fixture['expected_v2']['status'])
        counts[f'v1 {key[0]} -> v2 {key[1]}'] = counts.get(f'v1 {key[0]} -> v2 {key[1]}', 0) + 1
    table_exceptions = sorted(k for k in results if baselines[k] != results[k])
    summary = {'status': 'failed' if violations else 'passed', 'fixtures': len(fixtures),
               'mapping': counts, 'table_exceptions': table_exceptions, 'required_kinds': len(REQUIRED), 'covered_kinds': len(covered),
               'classifier_sha256': v1_sha, 'reference_classifier_sha256': reference['classifier_sha256'],
               'v2_results': v2_summary, 'violations': violations,
               'evaluated_at': datetime.now(timezone.utc).isoformat()}
    if args.receipt:
        args.receipt.parent.mkdir(parents=True, exist_ok=True)
        args.receipt.write_text(json.dumps(summary, indent=2) + '\n')
    print(json.dumps(summary, indent=2))
    return 1 if violations else 0


if __name__ == '__main__':
    sys.exit(main())
