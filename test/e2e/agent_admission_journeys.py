"""Real HTTP admission scenarios over isolated, synthetic Helm projects."""
from concurrent.futures import ThreadPoolExecutor
import json
from threading import Barrier

from agent_admission_fixture import operation


def request_body(**task_fields):
    return {'session_id': operation(), 'next_action': 'Run the synthetic checks',
            'finish_condition': 'All synthetic checks pass', **task_fields}


def start(world, project, token, title):
    body = request_body(title=title)
    return admitted(world.admit(project, token, body), body, 'started'), body


def admitted(response, body, action):
    admission = response.expect(200)['data']
    assert admission['action'] == action, 'incorrect admission action'
    assert admission.get('fence'), 'admission omitted its fence'
    for field in ('session_id', 'next_action', 'finish_condition'):
        assert admission[field] == body[field], f'admission changed {field}'
    task = admission['task']
    assert task.get('claimed_by') and task.get('claim_expires_at'), 'admitted task has no lease'
    if action == 'resumed':
        assert task['key'] == body['task_key'], 'admission resumed a different task'
    return task


def separate_tokens(world, project, prefix, count):
    return [world.token(world.agent(f'{prefix} {i + 1}', [project]), [project]) for i in range(count)]


def final_slot_and_retry(world, candidate, _baseline):
    with world.server(candidate):
        project = world.project('RACE')
        tokens = separate_tokens(world, project, 'Race agent', 6)
        for i, token in enumerate(tokens[:4]):
            start(world, project, token, f'Running fixture {i + 1}')
        world.have_capacity(project, 4)
        before = world.snapshot(project)

        bodies = [request_body(title=f'Fifth slot contender {i + 1}') for i in range(2)]
        keys = [operation(), operation()]
        ready = Barrier(2, timeout=5)

        def race(index):
            ready.wait()
            return world.admit(project, tokens[4 + index], bodies[index], key=keys[index])

        with ThreadPoolExecutor(2) as pool:
            responses = list(pool.map(race, range(2)))
        statuses = [response.status for response in responses]
        assert sorted(statuses) == [200, 409], f'expected one success and one conflict, got {statuses}'
        winner = statuses.index(200)
        loser = statuses.index(409)
        task = admitted(responses[winner], bodies[winner], 'started')
        responses[loser].expect(409, 'project_capacity_reached')
        world.have_capacity(project, 5)
        after = world.snapshot(project)
        assert len(after['tasks']) == len(before['tasks']) + 1, 'race left an orphan task'
        assert len(after['task_agent_admissions']) == len(before['task_agent_admissions']) + 1, (
            'race left an unexpected admission record'
        )
        assert after['project_counters'][0]['next_number'] == before['project_counters'][0]['next_number'] + 1, (
            'refusal consumed a project task number'
        )
        new_events = after['events'][len(before['events']):]
        assert {row['task_id'] for row in new_events} == {task['id']}, 'race added events unrelated to the winning card'
        tasks = world.call('GET', '/projects/RACE/tasks').expect(200)['data']
        assert task['title'] == bodies[winner]['title'], 'winning admission returned the wrong card'
        assert all(row['title'] != bodies[loser]['title'] for row in tasks), 'loser left a visible task'

        repeated = world.admit(project, tokens[4 + winner], bodies[winner], key=keys[winner])
        assert repeated.expect(200) == responses[winner].body, 'exact admission replay changed the response'
        changed = dict(bodies[winner], next_action='A different action under the same idempotency key')
        reused = world.admit(project, tokens[4 + winner], changed, key=keys[winner])
        reused.expect(409, 'idempotency_key_reused')
        again = world.admit(project, tokens[4 + loser], request_body(title='Full project denial'))
        again.expect(409, 'project_capacity_reached')
        assert world.snapshot(project) == after, 'replay or refusal changed project data'
        return {'active_before': 4, 'active_after': 5, 'responses': sorted(statuses),
                'new_task_rows': 1, 'winning_card_events': [row['type'] for row in new_events],
                'denial_code': again.code, 'idempotent_replay': True,
                'changed_replay_code': reused.code, 'refusal_preserved_project_rows': True}


def cross_project_claim(world, candidate, _baseline):
    with world.server(candidate):
        private = world.project('PRIVATEFIXTURE')
        target = world.project('DESTINATION')
        agent = world.agent('Cross-project fixture agent', [private, target])
        full_token = world.token(agent, [private, target])
        target_token = world.token(agent, [target])
        no_read_token = world.token(agent, [private, target], scopes=['tasks:write', 'tasks:claim'])
        task, body = start(world, private, full_token, 'Synthetic private work title')
        before = world.snapshot(target)

        target_body = request_body(title='Synthetic second project work')
        visible = world.admit(target, full_token, target_body)
        visible.expect(409, 'agent_already_active')
        assert visible.details.get('task_keys') == [task['key']], 'visible conflict omitted the active task key'
        scoped = world.admit(target, target_token, target_body)
        scoped.expect(409, 'agent_already_active')
        no_read = world.admit(target, no_read_token, target_body)
        no_read.expect(409, 'agent_already_active')
        private_fields = (private['id'], private['key'], task['id'], task['key'], task['title'], body['session_id'])
        for redacted in (scoped, no_read):
            assert not redacted.details.get('task_keys'), 'restricted conflict returned task keys'
            serialized = json.dumps(redacted.body)
            assert all(field not in serialized for field in private_fields), 'restricted conflict revealed private work'
        assert world.snapshot(target) == before, 'second-project refusal changed target project data'
        world.have_capacity(private, 1)
        world.have_capacity(target, 0)
        return {'visible_conflict': visible.code, 'scoped_conflict': scoped.code,
                'visible_conflict_keys': 1, 'scoped_conflict_keys': 0, 'no_read_conflict_keys': 0,
                'destination_active': 0, 'private_values_redacted': True}


def recover_stale_at_capacity(world, candidate, _baseline):
    with world.server(candidate):
        project = world.project('RECOVER')
        tokens = separate_tokens(world, project, 'Recovery agent', 6)
        for i, token in enumerate(tokens[:4]):
            start(world, project, token, f'Synthetic unaffected card {i + 1}')
        stale, original_body = start(world, project, tokens[4], 'Synthetic stalled card')
        world.have_capacity(project, 5)
        world.expire(stale)
        world.have_capacity(project, 5)
        before = world.snapshot(project)

        body = request_body(task_key=stale['key'])
        takeover = world.admit(project, tokens[5], body)
        recovered = admitted(takeover, body, 'resumed')
        fence = takeover.body['data']['fence']
        assert recovered['id'] == stale['id'], 'stale recovery replaced the task'
        assert recovered['claimed_by'] != stale['claimed_by'], 'stale recovery retained the old assignee as claimant'
        assert recovered['version'] > stale['version'], 'stale recovery did not advance the task version'
        world.have_capacity(project, 5)
        after = world.snapshot(project)
        assert len(after['tasks']) == len(before['tasks']), 'stale recovery inserted an extra task'
        current = world.resume_session(project, tokens[5], body['session_id']).expect(200)['data']
        assert current['task']['id'] == stale['id'] and current['fence'] == fence, 'session lookup changed the admission'
        world.resume_session(project, tokens[4], original_body['session_id']).expect(404, 'agent_admission_not_found')

        # Recover again with the same token. The old heartbeat or renewal
        # must fail even though the authenticated actor and token still match.
        world.expire(recovered)
        new_body = request_body(task_key=stale['key'])
        response = world.admit(project, tokens[5], new_body)
        fresh = admitted(response, new_body, 'resumed')
        fresh_fence = response.body['data']['fence']
        assert fresh_fence != fence, 'same-actor recovery reused the old fence'
        before_denials = world.snapshot(project)
        previous_renew = world.task_action(fresh, 'renew', {}, token=tokens[5], session=body['session_id'], fence=fence)
        previous_renew.expect(403, 'forbidden')
        world.task_action(fresh, 'renew', {}, token=tokens[5], session=new_body['session_id'], fence=fence).expect(403, 'forbidden')
        world.task_action(fresh, 'renew', {}, token=tokens[5]).expect(403, 'forbidden')
        assert world.snapshot(project) == before_denials, 'a refused renewal changed project data'
        world.task_action(fresh, 'renew', {}, token=tokens[5], session=new_body['session_id'], fence=fresh_fence).expect(200)
        world.have_capacity(project, 5)
        return {'response': 200, 'action': 'resumed', 'active_before': 5, 'active_after': 5,
                'new_task_rows': 0, 'replaced_claimant': True,
                'old_session_lookup_status': 404, 'old_session_renew_status': previous_renew.status,
                'same_credential_fence_rotated': True}


def upgrade_over_capacity(world, candidate, baseline):
    with world.server(baseline):
        project = world.project('UPGRADE')
        active = world.column(project, 'active')
        backlog = world.column(project, 'backlog')
        tasks = [world.task(project, f'Existing Active card {i + 1}', column_id=active,
                            description='Synthetic pre-upgrade history') for i in range(6)]
        deferred = world.task(project, 'Existing Backlog card', column_id=backlog)
        agent = world.agent('Legacy fixture agent', [project])
        token = world.token(agent, [project])
        tasks[0] = world.task_action(tasks[0], 'checklist', {'text': 'Retain existing criterion'}).expect(200)
        tasks[0] = world.task_action(tasks[0], 'claim', {}, token=token).expect(200)
        work = {'operation_id': 'legacy-upgrade', 'state': 'working', 'summary': 'Retain existing progress',
                'next_action': 'Continue after upgrade'}
        tasks[0] = world.task_action(tasks[0], 'progress', work, token=token).expect(200)
        before = world.snapshot(project)
        for table in ('comments', 'task_checklist_items', 'task_agent_work', 'task_agent_work_history'):
            assert before[table], f'legacy fixture lacks {table} data'

    with world.server(candidate):
        world.have_capacity(project, 6)
        assert world.snapshot(project) == before, 'starting the new server rewrote stored project data'
        world.task_action(tasks[0], 'renew', {}, token=token).expect(200)

        def reorder(task, source, destination):
            body = {'placement': 'first', 'expected_source_column_id': source,
                    'destination_column_id': destination, 'source': 'board'}
            return world.task_action(task, 'reorder', body)

        reorder(tasks[-1], active, active).expect(200)
        active_cards = world.call('GET', '/projects/UPGRADE/tasks?state=active').expect(200)['data']
        assert len(active_cards) == 6 and active_cards[0]['id'] == tasks[-1]['id'], 'legacy Active reorder did not persist'
        reorder(deferred, backlog, active).expect(409, 'project_capacity_reached')
        world.task_action(tasks[1], 'complete', {'comment': 'Synthetic over-capacity exit'}).expect(200)
        world.have_capacity(project, 5)
        at_capacity = world.snapshot(project)
        reorder(deferred, backlog, active).expect(409, 'project_capacity_reached')
        world.call('PATCH', f"/tasks/{deferred['id']}", {'column_id': active},
                   version=deferred['version']).expect(409, 'project_capacity_reached')
        world.call('POST', '/projects/UPGRADE/tasks', {'title': 'Synthetic denied creation',
                                                      'column_id': active}).expect(409, 'project_capacity_reached')
        assert world.snapshot(project) == at_capacity, 'refused human entry changed stored project rows'
        world.task_action(tasks[2], 'block', {'reason': 'Synthetic blocker opens an Active slot'}).expect(200)
        world.have_capacity(project, 4)
        reorder(deferred, backlog, active).expect(200)
        world.have_capacity(project, 5)
        return {'active_counts': [6, 5, 4, 5], 'preserved_rows': {table: len(rows) for table, rows in before.items()},
                'legacy_claim_renewed': True, 'same_column_reordered': True,
                'human_entry_conflicts': {'above_capacity': ['reorder'], 'at_capacity': ['reorder', 'patch', 'create']},
                'human_refusal_preserved_project_rows': True}
