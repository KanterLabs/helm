#!/usr/bin/env python3
"""Reproducible populated Helm database for neglect-signal scale work (plan case 16).

The real binary creates and migrates the schema and the projects, so the file
matches what that binary expects. Tasks, events, comments and progress
history are then bulk-inserted from a seeded generator on a fixed clock, and
the binary is restarted to prove it opens the result cleanly. Synthetic text
only. Output defaults to $TMPDIR; paths under /tmp are refused.

  python3 test/neglect/populate.py --binary .artifacts/neglect-helm            # 20k / 500k
  python3 test/neglect/populate.py --binary B --tasks 200 --events 5000 --output DB
"""
import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import random
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
from harness import Server  # noqa: E402

CLOCK = '2026-11-01T00:00:00Z'
CHANGED_FIELDS_SHIPPED = datetime(2026, 11, 7, tzinfo=timezone.utc)
HUMAN = 'actor-disabled-mode'
# (event type, weight). Legacy updates are emitted only before the changed_fields
# release; afterwards the same draw becomes an explicit metadata-only update.
EVENT_WEIGHTS = [
    ('task.updated:scope', 8), ('task.moved', 6), ('comment:human', 8), ('comment:agent', 6),
    ('task.checklist_item_added', 4), ('task.checklist_item_updated', 4),
    ('task.dependency_added', 1), ('progress', 8),
    ('task.claimed', 10), ('task.claim_renewed', 10), ('task.claim_released', 8),
    ('task.release_changed', 2), ('task.checklist_reordered', 1), ('task.updated:metadata', 6),
    ('task.updated:legacy', 10), ('task.parent_linked', 1), ('attachment.uploaded', 1),
]
COLUMN_WEIGHTS = [('backlog', 50), ('ready', 15), ('active', 10), ('blocked', 5), ('completed', 20)]
POISON = ['unknown_type', 'unparseable_time', 'malformed_payload']


def default_output(tasks, events, seed):
    scratch = Path(os.environ.get('TMPDIR') or '/var/tmp/agent-scratch-1000/tmp')
    return scratch / 'helm-neglect-bench' / f'bench-{tasks}t-{events}e-s{seed}.db'


class Generator:
    def __init__(self, args, projects, agent_id):
        self.rng = random.Random(args.seed)
        self.clock = datetime.fromisoformat(args.clock.replace('Z', '+00:00'))
        self.args, self.projects, self.agent_id = args, projects, agent_id
        self.digest = hashlib.sha256()
        self.symbols = {HUMAN: 'human', agent_id: 'agent'}
        for p_index, project in enumerate(projects):
            self.symbols[project['id']] = f'project{p_index}'
            for state, column in project['columns'].items():
                self.symbols[column] = f'column:{p_index}:{state}'

    def uid(self, *parts):
        return hashlib.sha256(':'.join(map(str, (self.args.seed,) + parts)).encode()).hexdigest()[:32]

    @staticmethod
    def at(value):
        return value.astimezone(timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.%f')[:-3] + 'Z'

    def record(self, table, row):
        self.digest.update(json.dumps([table] + [self.symbols.get(v, v) if isinstance(v, str) else v
                                                 for v in row]).encode())

    def plan(self):
        """Yield per-task plans; event counts (including extras) sum to --events."""
        n, total = self.args.tasks, self.args.events
        types, weights = zip(*EVENT_WEIGHTS)
        states, state_weights = zip(*COLUMN_WEIGHTS)
        poison = {self.rng.randrange(n): kind for kind in POISON for _ in range(self.args.poison)}
        shape = [(self.rng.choices(states, state_weights)[0], poison.get(i), self.rng.random() < .01)
                 for i in range(n)]
        # task.created plus the completion, deletion and poison rows each card needs.
        counts = [1 + (state == 'completed') + bool(bad) + deleted for state, bad, deleted in shape]
        if sum(counts) > total:
            raise SystemExit(f'--events must be at least {sum(counts)} for these tasks')
        for _ in range(total - sum(counts)):
            counts[self.rng.randrange(n)] += 1
        for index, (state, bad, deleted) in enumerate(shape):
            created = self.clock - timedelta(seconds=self.rng.randrange(1, 400 * 86400),
                                             milliseconds=self.rng.randrange(1000))
            span = (self.clock - created).total_seconds()
            extra = (state == 'completed') + bool(bad) + deleted
            offsets = sorted(self.rng.uniform(1, span) for _ in range(counts[index] - 1 - extra))
            kinds = self.rng.choices(types, weights, k=len(offsets))
            yield index, created, list(zip(offsets, kinds)), state, bad, deleted

    def rows(self):
        out = {'tasks': [], 'events': [], 'comments': [], 'progress': []}
        numbers = {p['id']: p['next_number'] for p in self.projects}
        for index, created, events, state, poison, deleted in self.plan():
            project = self.projects[index % len(self.projects)]
            task_id, number = self.uid('task', index), numbers[project['id']]
            numbers[project['id']] += 1
            stamps = [(created, 'task.created', {'number': number}, HUMAN, None)]
            items = []
            for offset, kind in events:
                when = created + timedelta(seconds=offset)
                stamps.append(self.event(task_id, len(stamps), when, kind, items))
            if poison:
                stamps.append(self.poison(created, poison))
            last = max(s[0] for s in stamps if isinstance(s[0], datetime))
            completed = self.at(last) if state == 'completed' else None
            if completed:
                stamps.append((last, 'task.completed', {}, HUMAN, None))
            if deleted:
                stamps.append((last, 'task.deleted', {'number': number}, HUMAN, None))
            out['tasks'].append((task_id, project['id'], number, project['columns'][state],
                                 f'Synthetic benchmark card {index}', float(number), len(stamps),
                                 completed, self.at(last) if deleted else None,
                                 self.at(created), self.at(last)))
            for seq, (when, kind, payload, actor, extra) in enumerate(stamps):
                stamp = when if isinstance(when, str) else self.at(when)
                event_id = self.uid('event', index, seq)
                text = payload if isinstance(payload, str) else json.dumps(payload, sort_keys=True)
                out['events'].append((stamp, event_id, kind, actor, project['id'], task_id, text, extra))
        for row in out['tasks']:
            self.record('tasks', row)
        out['events'].sort(key=lambda e: (e[0], e[1]))
        return out, numbers

    def event(self, task_id, seq, when, kind, items):
        base, _, variant = kind.partition(':')
        human = (HUMAN, None)
        if base == 'comment':
            comment_id = self.uid('comment', task_id, seq)
            actor = HUMAN if variant == 'human' else self.agent_id
            return when, 'comment.created', {'comment_id': comment_id}, actor, ('comment', comment_id)
        if base == 'progress':
            return when, 'task.progressed', {'phase': f'Phase {seq % 4}'}, self.agent_id, ('progress', seq % 4)
        if base == 'task.updated':
            if variant == 'scope':
                return (when, base, {'changed_fields': [self.rng.choice(['title', 'description'])]}) + human
            if variant == 'legacy' and when < CHANGED_FIELDS_SHIPPED:
                return (when, base, {'priority': 'high', 'version': seq + 1}) + human
            return (when, base, {'changed_fields': [], 'priority': 'normal'}) + human
        if base == 'task.checklist_item_added':
            items.append(self.uid('item', task_id, seq))
            return (when, base, {'item_id': items[-1], 'completed': False}) + human
        if base == 'task.checklist_item_updated':
            item = self.rng.choice(items) if items else self.uid('item', task_id, 'missing')
            return (when, base, {'item_id': item, 'completed': self.rng.random() < .7}) + human
        if base == 'task.moved':
            return (when, base, {'from_column_id': 'a', 'to_column_id': self.rng.choice('ab')}) + human
        if base.startswith('task.claim'):
            return when, base, {}, self.agent_id, None
        return (when, base, {}) + human

    def poison(self, created, kind):
        if kind == 'unknown_type':
            return created + timedelta(days=1), 'task.synthetic_future_kind', {}, HUMAN, None
        if kind == 'unparseable_time':
            return 'not-a-timestamp', 'task.updated', {'changed_fields': ['title']}, HUMAN, None
        return created + timedelta(days=1), 'task.updated', '{"changed_fields": ["title"', HUMAN, None


def insert(db, gen, data, numbers, first_cursor):
    db.executemany('INSERT INTO tasks(id,project_id,number,column_id,title,position,version,completed_at,'
                   'deleted_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)', data['tasks'])
    comments, progress, events = [], [], []
    for cursor, (stamp, event_id, kind, actor, project, task, payload, extra) in \
            enumerate(data['events'], first_cursor):
        events.append((cursor, event_id, kind, actor, project, task, payload, stamp))
        if extra and extra[0] == 'comment':
            comments.append((extra[1], task, actor, 'Synthetic benchmark comment', stamp, stamp))
        elif extra and extra[0] == 'progress':
            progress.append((gen.uid('history', event_id), task, gen.uid('operation', task), actor,
                             f'Phase {extra[1]}', 'Synthetic progress', stamp, stamp, cursor))
    for name, rows in (('events', events), ('comments', comments), ('progress', progress)):
        for row in rows:
            gen.record(name, row)
    db.executemany('INSERT INTO events(cursor,id,type,actor_id,project_id,task_id,payload,created_at) '
                   'VALUES (?,?,?,?,?,?,?,?)', events)
    db.executemany('INSERT INTO comments(id,task_id,actor_id,body,created_at,updated_at) '
                   'VALUES (?,?,?,?,?,?)', comments)
    db.executemany('INSERT INTO task_agent_work_history(id,task_id,operation_id,actor_id,state,phase,'
                   "summary,started_at,created_at,progress_event_cursor) VALUES (?,?,?,?,'working',?,?,?,?,?)",
                   progress)
    db.executemany('UPDATE project_counters SET next_number=? WHERE project_id=?',
                   [(number, project) for project, number in numbers.items()])
    return {'events': len(events), 'comments': len(comments), 'progress_history': len(progress)}


def build(args):
    output = Path(args.output or default_output(args.tasks, args.events, args.seed)).resolve()
    if str(output).startswith('/tmp/'):
        raise SystemExit('refusing /tmp; use $TMPDIR (/var/tmp/agent-scratch-1000/tmp)')
    if args.events < args.tasks:
        raise SystemExit('--events must be at least --tasks (one task.created each)')
    output.parent.mkdir(parents=True, exist_ok=True)
    for suffix in ('', '-wal', '-shm'):
        Path(str(output) + suffix).unlink(missing_ok=True)
    server = Server(args.binary, output.parent, name=output.stem, db_path=output)
    server.start()
    try:
        projects = []
        for index in range(args.projects or max(1, min(20, args.tasks // 1000))):
            project = server.api('POST', '/projects', {'key': f'BEN{index}', 'name': f'Benchmark {index}'},
                                 expected=201)
            columns = server.api('GET', f'/projects/BEN{index}/columns')['data']
            project['columns'] = {c['semantic_state']: c['id'] for c in reversed(columns)}
            projects.append(project)
        agent = server.api('POST', '/agents', {'name': 'Synthetic benchmark agent'}, expected=201)
    finally:
        server.stop()
    with server.db() as db:
        for project in projects:
            project['next_number'] = db.execute('SELECT next_number FROM project_counters WHERE project_id=?',
                                                (project['id'],)).fetchone()[0]
        first_cursor = db.execute('SELECT COALESCE(MAX(cursor),0)+1 FROM events').fetchone()[0]
    gen = Generator(args, projects, agent['id'])
    data, numbers = gen.rows()
    with server.db() as db:
        db.execute('PRAGMA foreign_keys=ON')
        counts = insert(db, gen, data, numbers, first_cursor)
    with server.db() as db:
        # Fold the bulk insert into the main file; the server's storage
        # readiness check treats a large WAL as exhausted capacity.
        db.execute('PRAGMA wal_checkpoint(TRUNCATE)')
    verify = reopen(server, projects[0], data['tasks'][0][0])
    with server.db() as db:
        # Since TC-379 the binary's projector runs during the API setup and the
        # reopen above. Empty its derived tables so the file is what an upgrade
        # from a pre-signal binary sees: migration 043 applied, no projection
        # row, no signal rows. Cases 8 and 16 then measure a complete seed.
        db.execute('DELETE FROM task_activity_signal')
        db.execute('DELETE FROM task_activity_signal_projection')
    with server.db() as db:
        db.execute('PRAGMA wal_checkpoint(TRUNCATE)')
        totals = {t: db.execute(f'SELECT COUNT(*) FROM {t}').fetchone()[0]
                  for t in ('tasks', 'events', 'comments', 'task_agent_work_history')}
        types = dict(db.execute('SELECT type, COUNT(*) FROM events GROUP BY type ORDER BY type').fetchall())
        schema = db.execute('SELECT MAX(version) FROM schema_migrations').fetchone()[0]
    manifest = {'database': str(output), 'seed': args.seed, 'clock': args.clock, 'tasks': args.tasks,
                'events_requested': args.events, 'generated': counts, 'table_totals': totals,
                'event_types': types, 'poison_per_kind': args.poison, 'schema_version': schema,
                'logical_sha256': gen.digest.hexdigest(), 'verify': verify,
                'generator_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                'binary_sha256': hashlib.sha256(Path(args.binary).read_bytes()).hexdigest()}
    Path(str(output) + '.manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    return manifest


def reopen(server, project, task_id):
    """Restart the binary on the populated file and read through the API."""
    seconds = server.start()
    try:
        page = server.api('GET', f'/projects/{project["key"]}/tasks?limit=50')
        status, _ = server.call('GET', f'/tasks/{task_id}/timeline?limit=100')
        return {'ready_seconds': round(seconds, 3), 'first_page_tasks': len(page['data']),
                'timeline_status': status}
    finally:
        server.stop()


def parser():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument('--binary', type=Path, required=True)
    p.add_argument('--tasks', type=int, default=20000)
    p.add_argument('--events', type=int, default=500000)
    p.add_argument('--projects', type=int, default=0, help='default: tasks/1000, between 1 and 20')
    p.add_argument('--seed', type=int, default=377)
    p.add_argument('--clock', default=CLOCK)
    p.add_argument('--poison', type=int, default=2, help='poison rows per kind (unknown type, '
                   'unparseable time, malformed payload)')
    p.add_argument('--output', type=Path)
    return p


if __name__ == '__main__':
    print(json.dumps(build(parser().parse_args()), indent=2))
