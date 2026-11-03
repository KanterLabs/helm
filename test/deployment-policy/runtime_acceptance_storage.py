"""Private local fixture preparation and comparison; never serializes rows."""
from __future__ import annotations
from contextlib import closing, contextmanager
import hashlib
import json
import os
from pathlib import Path
import secrets
import sqlite3

STAMP = '2026-01-01T00:00:00Z'
EXPIRES = '2099-01-01T00:00:00Z'
PREFIX = 'tc336-'


@contextmanager
def connect(path):
    db = sqlite3.connect(path)
    try:
        db.execute('PRAGMA foreign_keys=ON')
        with db:
            yield db
    finally:
        db.close()


def generated(path, prefix, repo):
    """A generated populated fixture, explicitly distinct from lane-copy proof."""
    with connect(path) as db:
        for migration in sorted((repo / 'internal/db/migrations').glob('*.sql')):
            version = int(migration.name.split('_')[0])
            if version > prefix:
                break
            db.executescript(migration.read_text())
            db.execute('INSERT INTO schema_migrations VALUES (?,?)', (version, STAMP))
        db.execute("INSERT INTO auth_setup VALUES (1,?)", (STAMP,))
        for ident, kind, admin in [('owner', 'human', 1), ('agent', 'agent', 0)]:
            db.execute('INSERT INTO actors(id,kind,name,email,admin,created_at,updated_at) '
                       'VALUES (?,?,?,?,?,?,?)', (ident, kind, ident, ident+'@example.test', admin, STAMP, STAMP))
        db.execute("INSERT INTO projects(id,key,slug,name,created_at,updated_at) VALUES ('project','TC336','tc336','Synthetic preservation project',?,?)", (STAMP, STAMP))
        db.execute("INSERT INTO columns(id,project_id,name,semantic_state,position,created_at,updated_at) VALUES ('column','project','Backlog','backlog',0,?,?)", (STAMP, STAMP))
        db.execute("INSERT INTO tasks(id,project_id,number,column_id,title,claimed_by,claim_expires_at,created_at,updated_at) VALUES ('task','project',1,'column','Synthetic preservation task','agent',?,?,?)", (EXPIRES, STAMP, STAMP))
        db.execute("INSERT INTO comments(id,task_id,actor_id,body,created_at,updated_at) VALUES ('comment','task','owner','Synthetic retained history',?,?)", (STAMP, STAMP))
        db.execute("INSERT INTO events(id,type,actor_id,project_id,task_id,payload,created_at) VALUES ('event','task.created','owner','project','task','{}',?)", (STAMP,))
        db.execute("INSERT INTO actor_projects VALUES ('agent','project')")
        db.execute("INSERT INTO project_counters VALUES ('project',2)")
    os.chmod(path, 0o600)


def copy_database(source, dest, prefix):
    with closing(sqlite3.connect(f'file:{source}?mode=ro', uri=True)) as original, connect(dest) as db:
        assert original.execute('SELECT MAX(version) FROM schema_migrations').fetchone()[0] == prefix, 'wrong input schema'
        original.backup(db)
    os.chmod(dest, 0o600)


def snapshot(path, prefix):
    """Keep comparison data in memory only; ignore documented volatile fields."""
    result = {}
    excluded = {'sessions': {'last_seen_at'}, 'tokens': {'last_used_at'},
                'actors': {'last_seen_at'}}
    if prefix == 35:
        excluded['columns'] = {'position', 'version', 'updated_at'}  # migration036
    with connect(path) as db:
        for (table,) in db.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"):
            if table == 'schema_migrations':
                continue
            columns = [r[1] for r in db.execute(f'PRAGMA table_info("{table}")')
                       if r[1] not in excluded.get(table, set())]
            quoted = ','.join('"'+c+'"' for c in columns)
            result[table] = (columns, list(db.execute(f'SELECT {quoted} FROM "{table}"')))
    return result


def preserved(path, previous, expected_schema=41):
    with connect(path) as db:
        assert db.execute('PRAGMA integrity_check').fetchall() == [('ok',)], 'SQLite integrity failed'
        assert db.execute('PRAGMA foreign_key_check').fetchall() == [], 'foreign key violation'
        assert db.execute('SELECT MAX(version) FROM schema_migrations').fetchone()[0] == expected_schema
        for table, (columns, rows) in previous.items():
            quoted = ','.join('"'+c+'"' for c in columns)
            current = set(db.execute(f'SELECT {quoted} FROM "{table}"'))
            assert all(row in current for row in rows), f'existing values changed: {table}'
    return {'integrity': 'ok', 'foreign_key_violations': 0,
            'preserved_tables': len(previous), 'schema_version': expected_schema}


def identities(path):
    """Seed disposable credentials only on the isolated pre-migration copy."""
    import bcrypt
    password = 'TC336-'+secrets.token_urlsafe(20)
    hashed = bcrypt.hashpw(password.encode(), bcrypt.gensalt()).decode()
    actors = {}
    with connect(path) as db:
        for name, admin in [('admin', 1), ('restricted', 0), ('zero', 0), ('pending', 0), ('disabled', 0), ('configured', 1)]:
            ident = PREFIX+name
            email = ident+'@example.test'
            db.execute('INSERT INTO actors(id,kind,name,email,password_hash,admin,created_at,updated_at) VALUES (?,\'human\',?,?,?,?,?,?)', (ident, name, email, hashed, admin, STAMP, STAMP))
            cookie = secrets.token_urlsafe(32)
            db.execute('INSERT INTO sessions(id,actor_id,token_hash,expires_at,created_at,last_seen_at) VALUES (?,?,?,?,?,?)', (ident, ident, hashlib.sha256(cookie.encode()).hexdigest(), EXPIRES, STAMP, STAMP))
            actors[name] = {'id': ident, 'email': email, 'password': password, 'cookie': cookie}
    return actors


def bearer(path, actor, project_ids, scopes):
    raw = secrets.token_urlsafe(32)
    with connect(path) as db:
        db.execute('INSERT INTO tokens(id,actor_id,name,token_hash,scopes,project_ids,created_at) VALUES (?,?,?,?,?,?,?)',
                   (secrets.token_hex(16), actor, 'Synthetic acceptance', hashlib.sha256(raw.encode()).hexdigest(), json.dumps(scopes), json.dumps(project_ids), STAMP))
    return raw


def pending(path, actor):
    with connect(path) as db:
        db.execute('DELETE FROM project_memberships WHERE actor_id=?', (actor,))
        db.execute('DELETE FROM workspace_memberships WHERE actor_id=?', (actor,))
