"""Synthetic I/O for the real controller; no sockets, hosts or credentials."""
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sys
import time
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'deploy'))
spec = importlib.util.spec_from_file_location('coolify_deploy', ROOT / 'deploy/coolify-deploy.py')
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)


def pair(letter):
    return {'image': 'ghcr.io/kanterlabs/helm@sha256:' + letter * 64, 'sha': letter * 40}


OLD, CANDIDATE, RECOVERY = pair('a'), pair('b'), pair('c')
CAPABILITIES = ['membership-authority-v1', 'legacy-session-invalidation-v1', 'current-data-recovery-v1']
SCENARIOS = ['restricted-human', 'zero-grant-human', 'pending-disabled-human',
             'configured-email-demotion', 'bearer-scope-intersection', 'aggregate-visibility',
             'session-revoke-reset', 'disable-reactivation', 'schema35-populated-upgrade',
             'schema40-populated-upgrade', 'current-data-recovery-rollforward']
TARGET = {'lane': 'beta', 'service_uuid': 'fixture-service', 'service_name': 'fixture-beta',
          'project_uuid': 'fixture-project', 'environment_uuid': 'fixture-environment',
          'server_uuid': 'fixture-server', 'site_url': 'https://fixture.home.shanekanterman.dev',
          'api_url': 'https://coolify.home.shanekanterman.dev/api/v1',
          'data_identity': 'fixture-volume/database.sqlite'}


def write_json(path, value):
    raw = (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()
    path.write_bytes(raw)
    return hashlib.sha256(raw).hexdigest()


class World:
    def __init__(self, root):
        self.root = root
        self.calls, self.starts, self.observations = [], [], []
        self.active = OLD.copy()
        self.status = 'running:healthy'
        self.database = {'identity': TARGET['data_identity'], 'schema': 40, 'writes': ['retained']}
        self.floor = {'format': 1, 'minimum': 0, 'target': TARGET.copy()}
        self.envs = {'HELM_IMAGE': OLD['image'], 'HELM_RELEASE_SHA': OLD['sha'],
                     'HELM_AUTHORIZATION_FLOOR': json.dumps(self.floor)}
        self.fail = None
        self.failed = False
        self.bad_candidate = False
        self.bad_recovery = False
        self.witness_fault = None
        self.bundle = {'format': 1, 'kind': 'helm-runtime-acceptance-v1',
                       'producer': 'tc336-isolated-runtime-v1', 'issued_at': int(time.time()) - 1,
                       'expires_at': int(time.time()) + 3600, 'target': TARGET.copy(), 'artifacts': {}}
        for role, subject in [('candidate', CANDIDATE), ('recovery', RECOVERY)]:
            proofs = {}
            for scenario in SCENARIOS:
                name = role + '-' + scenario + '.json'
                exchange = {'method': 'GET', 'path': '/api/v1/projects', 'status': 403,
                            'expected_status': 403, 'response': {'error': 'forbidden'},
                            'expected_response': {'error': 'forbidden'}}
                checksum = write_json(root / name, {'format': 1, 'scenario': scenario,
                    'target': TARGET.copy(), 'subject': subject, 'result': 'passed',
                    'observations': [{'assertion': 'synthetic runtime contract', 'passed': True,
                                      'exchange': exchange}]})
                proofs[scenario] = {'file': name, 'sha256': checksum}
            self.bundle['artifacts'][role] = {**subject, 'capabilities': CAPABILITIES.copy(), 'proofs': proofs}
        self.helper = root / 'observer'
        self.helper.write_text('#!/bin/sh\nexit 1\n')
        self.helper.chmod(0o700)
        self.settings = {'COOLIFY_API_URL': TARGET['api_url'], 'COOLIFY_API_TOKEN': 'fixture-secret',
            'COOLIFY_SERVICE_UUID': TARGET['service_uuid'], 'COOLIFY_SERVICE_NAME': TARGET['service_name'],
            'COOLIFY_ENVIRONMENT_UUID': TARGET['environment_uuid'], 'COOLIFY_PROJECT_UUID': TARGET['project_uuid'],
            'COOLIFY_SERVER_UUID': TARGET['server_uuid'], 'HELM_SITE_URL': TARGET['site_url'],
            'HELM_IMAGE': CANDIDATE['image'], 'HELM_RELEASE_SHA': CANDIDATE['sha'],
            'HELM_DEPLOY_EVIDENCE': str(root / 'deployment.json'), 'HELM_DEPLOY_LANE': 'beta',
            'HELM_DATA_IDENTITY': TARGET['data_identity'], 'HELM_RECOVERY_IMAGE': RECOVERY['image'],
            'HELM_RECOVERY_SHA': RECOVERY['sha'], 'HELM_ACCEPTANCE_BUNDLE': str(root / 'bundle.json'),
            'HELM_STOP_OBSERVER': str(self.helper),
            'HELM_STOP_OBSERVER_SHA256': hashlib.sha256(self.helper.read_bytes()).hexdigest()}
        self.seal()

    def seal(self):
        self.settings['HELM_ACCEPTANCE_SHA256'] = write_json(self.root / 'bundle.json', self.bundle)

    def observe(self, request):
        self.observations.append(copy.deepcopy(request))
        now = int(time.time())
        value = {**request, 'format': 1, 'observed_at': now, 'expires_at': now + 60,
                 'result': 'stopped-and-fenced', 'running_writers': [], 'restart_eligible': [],
                 'queued_operations': [], 'fence': 'fixture-exclusive-fence',
                 'container_ids': ['fixture-old-container']}
        if self.witness_fault:
            self.witness_fault(value)
        return value

    def request(self, method, path, body=None, *, route=False):
        safe = {'method': method, 'path': path}
        if body and method == 'PATCH':
            safe.update(key=body['key'], value=body['value'])
        self.calls.append(safe)
        service = '/services/' + TARGET['service_uuid']
        if route:
            return {'service': 'helm', 'status': 'ok', 'ready': True,
                    'revision': self.active['sha'] if self.active else 'stopped'}
        if method == 'GET' and path == service:
            return {'uuid': TARGET['service_uuid'], 'name': TARGET['service_name'],
                    'server': {'uuid': TARGET['server_uuid']}, 'status': self.status,
                    'applications': [{'name': 'helm', 'status': self.status}], 'databases': []}
        if method == 'GET' and path.startswith('/projects/'):
            return {'uuid': TARGET['environment_uuid'], 'services': [
                {'uuid': TARGET['service_uuid'], 'name': TARGET['service_name']}]}
        if method == 'GET' and path == service + '/envs':
            return [{'key': key, 'value': value} for key, value in self.envs.items()]
        if method == 'PATCH' and path == service + '/envs':
            key = body['key']
            fault = self.fail == key and not self.failed
            if fault:
                self.failed = True
            if not fault or getattr(self, 'lost_response', False):
                self.envs[key] = body['value']
            if fault:
                raise deploy.DeployError('synthetic PATCH failure')
            return {}
        if method == 'POST' and path == service + '/stop?docker_cleanup=false':
            self.active = None
            self.status = 'exited'
            return {'message': 'queued'}
        if method == 'POST' and (path == service + '/start' or '/restart' in path):
            self.active = {'image': self.envs['HELM_IMAGE'], 'sha': self.envs['HELM_RELEASE_SHA']}
            self.starts.append(self.active.copy())
            self.status = 'running:healthy'
            if self.active == CANDIDATE:
                self.database['schema'] = 41
                if 'candidate-write' not in self.database['writes']:
                    self.database['writes'].append('candidate-write')
            return {}
        raise AssertionError((method, path))

    def run(self):
        try:
            with patch.dict(os.environ, self.settings, clear=True):
                controller = deploy.Deployer()
        except Exception as exc:
            return str(exc), {'status': 'constructor-rejected'}
        controller.request = self.request
        if getattr(self, 'short_wait', False):
            controller.timeout, controller.poll = 0.02, 0
        # Only transport is synthetic; contract parsing/nonce/fence validation stays real.
        if hasattr(controller, 'observe_transport') and not getattr(self, 'real_observer', False):
            controller.observe_transport = self.observe
        original_wait = controller.wait

        def wait(image, sha):
            if (image == CANDIDATE['image'] and self.bad_candidate) or (
                    image == RECOVERY['image'] and self.bad_recovery):
                raise deploy.DeployError('synthetic readiness failure after migration')
            return original_wait(image, sha)

        controller.wait = wait
        error = None
        try:
            controller.deploy()
        except (deploy.DeployError, ValueError, OSError) as exc:
            error = str(exc)
        return error, controller.evidence

    def mutations(self):
        return [call for call in self.calls if call['method'] != 'GET']
