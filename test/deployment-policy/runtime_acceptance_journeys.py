"""Real HTTP authorization/session journeys and same-current-data rollforward."""
from __future__ import annotations
import json
import secrets
import runtime_acceptance_storage as storage


def member_patch(app, ident, patch, admin):
    return app.call('PATCH', '/api/v1/members/'+ident, 200, patch, cookie=admin,
                    template='/api/v1/members/{actor_id}')


def project(app, key, admin):
    return app.call('POST', '/api/v1/projects', 201,
                    {'key': key, 'name': 'Synthetic '+key}, cookie=admin)[0]['id']


def exercise(app, identities, db):
    a = identities
    app.scenario = 'schema'+('35' if 'schema35' in str(db) else '40')+'-populated-upgrade'
    app.call('GET', '/readyz', 200)
    # This cookie predates migration041; the real retained image authenticates it.
    admin = a['admin']['cookie']
    app.call('GET', '/api/v1/members', 200, cookie=admin)
    grant = project(app, 'G'+secrets.token_hex(4).upper(), admin)
    hidden = project(app, 'H'+secrets.token_hex(4).upper(), admin)
    app.call('POST', '/api/v1/projects/'+grant+'/tasks', 201,
             {'title': 'Synthetic granted work'}, cookie=admin, template='/api/v1/projects/{project_id}/tasks')
    app.call('POST', '/api/v1/projects/'+hidden+'/tasks', 201,
             {'title': 'Synthetic forbidden work'}, cookie=admin, template='/api/v1/projects/{project_id}/tasks')
    app.scenario = 'restricted-human'
    member_patch(app, a['restricted']['id'], {'project_ids': [grant]}, admin)
    restricted = app.login(a['restricted'])
    app.call('GET', '/api/v1/projects/'+grant, 200, cookie=restricted, template='/api/v1/projects/{project_id}')
    app.call('GET', '/api/v1/projects/'+hidden, 403, cookie=restricted, template='/api/v1/projects/{project_id}')
    app.call('GET', '/api/v1/members', 403, cookie=restricted)
    app.scenario = 'zero-grant-human'
    member_patch(app, a['zero']['id'], {'project_ids': []}, admin)
    zero = app.login(a['zero'])
    app.call('GET', '/api/v1/projects', 200, cookie=zero, expected={'data_count': 0})
    app.call('GET', '/api/v1/projects/'+grant, 403, cookie=zero, template='/api/v1/projects/{project_id}')
    app.scenario = 'aggregate-visibility'
    with storage.connect(db) as sql:
        all_projects = [row[0] for row in sql.execute('SELECT id FROM projects')]
    forbidden = [ident for ident in all_projects if ident != grant]
    for route in ['/projects', '/search?q=Synthetic', '/intake', '/sidebar-counts', '/my-work', '/notifications', '/views', '/export', '/events', '/roadmap', '/tickets']:
        expected_restricted = {'data_count': 1} if route == '/projects' else {'task_total': 1} if route == '/roadmap' else {'issues': 0, 'my_work': 0} if route == '/sidebar-counts' else None
        expected_zero = {'data_count': 0} if route == '/projects' else {'task_total': 0} if route == '/roadmap' else {'issues': 0, 'my_work': 0} if route == '/sidebar-counts' else None
        app.call('GET', '/api/v1'+route, 200, cookie=restricted, project=forbidden, expected=expected_restricted)
        app.call('GET', '/api/v1'+route, 200, cookie=zero, project=all_projects, expected=expected_zero)
    # Pending is an explicit non-member identity, never a mocked HTTP response.
    app.scenario = 'pending-disabled-human'
    pending_cookie = app.login(a['pending'])
    storage.pending(db, a['pending']['id'])
    app.call('GET', '/api/v1/projects', 401, cookie=pending_cookie)
    app.call('GET', '/api/v1/export', 401, cookie=pending_cookie)
    member_patch(app, a['disabled']['id'], {'disabled': True}, admin)
    app.login(a['disabled'], 401)
    app.call('GET', '/api/v1/projects', 401, cookie=a['disabled']['cookie'])
    app.scenario = 'bearer-scope-intersection'
    scopes = ['projects:read', 'tasks:read', 'events:read']
    human = storage.bearer(db, a['restricted']['id'], [grant, hidden], scopes)
    app.call('GET', '/api/v1/projects/'+grant, 200, bearer=human, template='/api/v1/projects/{project_id}')
    app.call('GET', '/api/v1/projects/'+hidden, 403, bearer=human, template='/api/v1/projects/{project_id}')
    app.call('POST', '/api/v1/projects/'+grant+'/tasks', 403,
             {'title': 'Synthetic forbidden write'}, bearer=human, template='/api/v1/projects/{project_id}/tasks')
    narrow = storage.bearer(db, a['restricted']['id'], [hidden], scopes)
    app.call('GET', '/api/v1/projects/'+grant, 403, bearer=narrow, template='/api/v1/projects/{project_id}')
    agent, _ = app.call('POST', '/api/v1/agents', 201, {'name': 'Synthetic acceptance agent', 'project_ids': [grant]}, cookie=admin)
    agent_token = storage.bearer(db, agent['id'], [grant, hidden], scopes)
    app.call('GET', '/api/v1/projects/'+grant, 200, bearer=agent_token, template='/api/v1/projects/{project_id}')
    app.call('GET', '/api/v1/projects/'+hidden, 403, bearer=agent_token, template='/api/v1/projects/{project_id}')
    app.call('GET', '/api/v1/members', 403, bearer=agent_token)
    app.scenario = 'session-revoke-reset'
    first = app.login(a['restricted'])
    second = app.login(a['restricted'])
    app.call('GET', '/api/v1/auth/sessions', 200, cookie=first)
    # Select this exact login's session by its private token hash in local DB.
    import hashlib
    with storage.connect(db) as sql:
        session_id = sql.execute('SELECT id FROM sessions WHERE token_hash=?', (hashlib.sha256(first.encode()).hexdigest(),)).fetchone()[0]
    app.call('DELETE', '/api/v1/auth/sessions/'+session_id, 204, cookie=first, template='/api/v1/auth/sessions/{session_id}')
    app.call('GET', '/api/v1/projects', 401, cookie=first)
    app.call('GET', '/api/v1/projects', 200, cookie=second)
    new_password = 'TC336-'+secrets.token_urlsafe(20)
    app.call('POST', '/api/v1/members/'+a['restricted']['id']+'/password', 200,
             {'password': new_password}, cookie=admin, template='/api/v1/members/{actor_id}/password')
    app.call('GET', '/api/v1/projects', 401, cookie=second)
    app.call('GET', '/api/v1/projects', 401, cookie=a['restricted']['cookie'])
    a['restricted']['password'] = new_password
    third = app.login(a['restricted'])
    app.scenario = 'disable-reactivation'
    member_patch(app, a['restricted']['id'], {'disabled': True}, admin)
    app.call('GET', '/api/v1/projects', 401, cookie=third)
    app.login(a['restricted'], 401)
    member_patch(app, a['restricted']['id'], {'disabled': False}, admin)
    app.call('GET', '/api/v1/projects', 401, cookie=third)
    fresh = app.login(a['restricted'])
    app.call('GET', '/api/v1/projects/'+grant, 200, cookie=fresh, template='/api/v1/projects/{project_id}')
    app.call('GET', '/api/v1/projects/'+hidden, 403, cookie=fresh, template='/api/v1/projects/{project_id}')


def rollforward(args, root, network, Runtime, summary):
    # Use the recovery's already-migrated, exercised current data; no DB restore.
    for version in [35, 40]:
        work = root/f'recovery-schema{version}'
        db = work/'roadmap.db'
        with storage.connect(db) as sql:
            before_members = list(sql.execute('SELECT * FROM workspace_memberships ORDER BY actor_id'))
            before_grants = list(sql.execute('SELECT * FROM project_memberships ORDER BY actor_id,project_id'))
            raw = secrets.token_urlsafe(32)
            import hashlib
            sql.execute('INSERT INTO sessions(id,actor_id,token_hash,expires_at,created_at,last_seen_at) VALUES (?,?,?,?,?,?)',
                        ('tc336-rollforward', 'tc336-admin', hashlib.sha256(raw.encode()).hexdigest(), storage.EXPIRES, storage.STAMP, storage.STAMP))
        recovery = Runtime(args.recovery, work, network, args.recovery_sha)
        candidate = Runtime(args.candidate, work, network, args.candidate_sha)
        try:
            recovery.start()
            recovery.scenario = 'current-data-recovery-rollforward'
            key = 'R'+secrets.token_hex(4).upper()
            ident = project(recovery, key, raw)
            # Capture history after authorized recovery write, privately in memory.
            with storage.connect(db) as sql:
                events = list(sql.execute('SELECT id,type,payload FROM events'))
                before_members = list(sql.execute('SELECT * FROM workspace_memberships ORDER BY actor_id'))
                before_grants = list(sql.execute('SELECT * FROM project_memberships ORDER BY actor_id,project_id'))
            recovery.stop()
            candidate.start()
            candidate.scenario = 'current-data-recovery-rollforward'
            candidate.call('GET', '/api/v1/projects/'+ident, 200, cookie=raw,
                           expected={'key': key}, template='/api/v1/projects/{project_id}')
            with storage.connect(db) as sql:
                assert before_members == list(sql.execute('SELECT * FROM workspace_memberships ORDER BY actor_id'))
                assert before_grants == list(sql.execute('SELECT * FROM project_memberships ORDER BY actor_id,project_id'))
                after_events = set(sql.execute('SELECT id,type,payload FROM events'))
                assert all(event in after_events for event in events), 'recovery write history lost'
            summary.setdefault('rollforward', []).append({'schema_origin': version, 'result': 'passed',
                'observations': recovery.observations+candidate.observations,
                'same_current_database': True, 'memberships_grants_history_preserved': True})
        finally:
            recovery.stop()
            candidate.stop()
