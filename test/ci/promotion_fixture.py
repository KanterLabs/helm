"""Synthetic Git/GitHub boundary fixture; never contacts an external service."""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import zipfile

HERE = Path(__file__).resolve().parent
REPOSITORY = 'KanterLabs/helm'
IMAGE = 'ghcr.io/kanterlabs/helm@sha256:' + 'a' * 64
WORKFLOW = '.github/workflows/ci.yml'
FAKE_TOOL = '''#!/usr/bin/env python3
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
if name != 'gh':
    print({'go':'go version go1.26.10 linux/amd64','node':'v22.23.2',
           'npm':'11.0.0','docker':'Docker version 28.0.0'}[name])
    raise SystemExit
endpoint = sys.argv[-1]
with open(os.environ['PROMOTION_API_LOG'], 'a') as out:
    out.write(endpoint + '\\n')
fixture = json.loads(pathlib.Path(os.environ['PROMOTION_API_FIXTURE']).read_text())
if endpoint not in fixture:
    raise SystemExit(1)
value = fixture[endpoint]
if isinstance(value, dict) and '__bytes__' in value:
    sys.stdout.buffer.write(pathlib.Path(value['__bytes__']).read_bytes())
else:
    print(json.dumps(value))
'''


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True).strip()


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value) if isinstance(value, (dict, list)) else value)


class Fixture:
    def __init__(self, root):
        self.base = Path(root)
        self.repo = self.base / 'repo'
        self.repo.mkdir()
        git(self.repo, 'init', '-q', '-b', 'beta')
        git(self.repo, 'config', 'user.email', 'fixture@example.invalid')
        git(self.repo, 'config', 'user.name', 'Synthetic CI fixture')
        files = {'go.mod': 'module example.invalid/helm\ngo 1.26.10\n',
                 'go.sum': '', 'web/package.json': '{}', 'web/package-lock.json': '{}',
                 WORKFLOW: 'name: trusted validation\n', 'test/ci/suites.json': '{}',
                 'web/e2e/task.spec.ts': '// synthetic inventory\n',
                 'Dockerfile': 'FROM scratch\n', 'VERSION': '0.2.2\n'}
        for path, content in files.items():
            write(self.repo / path, content)
        git(self.repo, 'add', '.')
        git(self.repo, 'commit', '-qm', 'Synthetic validation source')
        self.sha = git(self.repo, 'rev-parse', 'HEAD')
        self.tree = git(self.repo, 'rev-parse', 'HEAD^{tree}')
        self.bin = self.base / 'tools'
        self.bin.mkdir()
        for name in ['gh', 'go', 'node', 'npm', 'docker']:
            write(self.bin / name, FAKE_TOOL)
            (self.bin / name).chmod(0o755)
        self.settings = self.base / 'settings.json'
        write(self.settings, {'go_flags': '-trimpath', 'ldflags': '-s -w',
                              'cgo_enabled': '0', 'docker_platform': 'linux/amd64'})
        self.validation = self.base / 'verified.json'
        self.inventory = ['e2e/task.spec.ts', 'github']
        write(self.validation, {'format': 1, 'schema_version': 1, 'accepted': True,
             'passed': True, 'scope': 'full', 'effective_scope': 'full',
             'source': {'sha': self.sha, 'tree': self.tree},
             'source_sha': self.sha, 'source_tree': self.tree,
             'selected_suites': self.inventory, 'executed_suites': self.inventory,
             'results': {name: 'success' for name in ['checks', 'browser', 'race', 'container']},
             'policy_hash': '1' * 64, 'plan_sha256': '2' * 64, 'evidence_sha256': '3' * 64,
             'browser': {'actual_inventory': self.inventory, 'digests': {'report': '4' * 64}}})
        write(self.base / 'helm', b'synthetic executable'.decode())
        write(self.base / 'frontend/index.html', '<p>synthetic frontend</p>')
        self.output = self.base / 'validation.json'
        self.api_file = self.base / 'api.json'
        self.log = self.base / 'api.log'
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'],
                        GITHUB_REPOSITORY=REPOSITORY, GITHUB_EVENT_NAME='push',
                        GITHUB_REF='refs/heads/beta', GITHUB_SHA=self.sha,
                        GITHUB_RUN_ID='123', GITHUB_RUN_ATTEMPT='1',
                        GITHUB_WORKFLOW_SHA=self.sha,
                        GITHUB_WORKFLOW_REF=REPOSITORY + '/' + WORKFLOW + '@refs/heads/beta',
                        PROMOTION_API_FIXTURE=str(self.api_file), PROMOTION_API_LOG=str(self.log))
        self.api = {}

    def create_args(self):
        return ['create', '--root', str(self.repo), '--validation', str(self.validation),
                '--image', IMAGE, '--binary', str(self.base / 'helm'), '--frontend',
                str(self.base / 'frontend'), '--build-settings', str(self.settings),
                '--output', str(self.output)]

    def invoke(self, script, args):
        return subprocess.run([sys.executable, str(HERE / script), *args], env=self.env,
                              capture_output=True, text=True, timeout=30)

    def produce(self):
        result = self.invoke('receipt.py', self.create_args())
        if result.returncode:
            raise AssertionError(result.stderr + result.stdout)
        return json.loads(self.output.read_text())

    def prepare_api(self, receipt=None):
        receipt = receipt or self.produce()
        archive = io.BytesIO()
        with zipfile.ZipFile(archive, 'w') as out:
            out.writestr('validation.json', json.dumps(receipt))
        self.archive = self.base / 'artifact.zip'
        self.archive.write_bytes(archive.getvalue())
        run = {'id': 123, 'run_attempt': 1, 'event': 'push', 'head_branch': 'beta',
               'head_sha': self.sha, 'status': 'completed', 'conclusion': 'success',
               'path': WORKFLOW, 'workflow_id': 42, 'repository': {'full_name': REPOSITORY},
               'head_repository': {'full_name': REPOSITORY}}
        artifact = {'id': 456, 'name': 'helm-validation-' + self.sha + '-1',
                    'expired': False, 'expires_at': '2099-01-01T00:00:00Z',
                    'digest': 'sha256:' + hashlib.sha256(archive.getvalue()).hexdigest(),
                    'workflow_run': {'id': 123, 'head_sha': self.sha, 'head_branch': 'beta'}}
        prefix = 'repos/' + REPOSITORY + '/actions/'
        self.run_endpoint = prefix + 'runs/123'
        self.artifact_endpoint = prefix + 'runs/123/artifacts?per_page=100'
        self.jobs_endpoint = prefix + 'runs/123/attempts/1/jobs?per_page=100'
        self.api = {self.run_endpoint: run,
                    prefix + 'workflows/42': {'id': 42, 'path': WORKFLOW, 'state': 'active'},
                    self.artifact_endpoint: {'total_count': 1, 'artifacts': [artifact]},
                    self.jobs_endpoint: {'total_count': 4, 'jobs': [
                        {'name': name, 'status': 'completed', 'conclusion': 'success',
                         'run_id': 123, 'head_sha': self.sha}
                        for name in ['checks', 'browser', 'race', 'container']]},
                    prefix + 'artifacts/456/zip': {'__bytes__': str(self.archive)}}
        self.flush()
        return receipt

    def flush(self):
        write(self.api_file, self.api)

    def resolve(self):
        self.flush()
        result = self.invoke('promotion.py', ['resolve', '--root', str(self.repo),
                             '--run-id', '123', '--build-settings', str(self.settings)])
        if result.returncode:
            raise AssertionError(result.stderr + result.stdout)
        return json.loads(result.stdout)
