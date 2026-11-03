"""Run the real image publisher against a recording Docker executable."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
ARTIFACTS = ROOT / '.artifacts/ci-workflows'
SHA = 'a' * 40
DIGEST = 'ghcr.io/kanterlabs/helm@sha256:' + 'b' * 64
TOKEN = 'synthetic-login-credential-never-capture'
FAKE_DOCKER = r'''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys
args = sys.argv[1:]
entry = {'argv': args, 'docker_config': os.environ.get('DOCKER_CONFIG', '')}
if args and args[0] == 'login':
    entry['stdin_present'] = bool(sys.stdin.read())
with open(os.environ['DOCKER_LOG'], 'a') as stream:
    stream.write(json.dumps(entry) + '\n')
if args and args[0] == 'push' and os.environ.get('MOCK_PUSH_FAILURE') == 'true':
    sys.exit(17)
if args[:2] == ['image', 'inspect']:
    if any('org.opencontainers.image.revision' in arg for arg in args):
        print(os.environ.get('MOCK_REVISION', ''))
    else:
        print(os.environ.get('MOCK_DIGEST', ''))
'''


class ImagePublisherJourneys(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.bin = self.directory / 'bin'
        self.bin.mkdir()
        fake = self.bin / 'docker'
        fake.write_text(FAKE_DOCKER)
        fake.chmod(0o755)
        self.images = self.directory / 'image'
        self.images.mkdir()
        self.archive = self.images / ('helm-' + SHA + '.tar.gz')
        self.archive.write_bytes(b'synthetic compressed image bytes\n')
        (self.images / (self.archive.name + '.sha256')).write_text(
            hashlib.sha256(self.archive.read_bytes()).hexdigest() + '  ' + self.archive.name + '\n')
        self.log = self.directory / 'docker.jsonl'
        self.output = self.directory / 'github-output'
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'],
                        GITHUB_EVENT_NAME='push', GITHUB_REF='refs/heads/beta', GITHUB_SHA=SHA,
                        IMAGE_DIR=str(self.images), GHCR_TOKEN=TOKEN,
                        GITHUB_OUTPUT=str(self.output), GITHUB_ACTOR='fixture-actor',
                        RUNNER_TEMP=str(self.directory), DOCKER_LOG=str(self.log),
                        MOCK_REVISION=SHA, MOCK_DIGEST=DIGEST)
        self.env.pop('DOCKER_CONFIG', None)
        self.sequence = 0
        ARTIFACTS.mkdir(parents=True, exist_ok=True)

    def run_publisher(self, success, **overrides):
        self.sequence += 1
        self.log.unlink(missing_ok=True)
        self.output.unlink(missing_ok=True)
        result = subprocess.run(['bash', str(ROOT / 'test/ci/publish-image.sh')],
                                env=dict(self.env, **overrides), text=True, capture_output=True)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []
        output = self.output.read_text() if self.output.exists() else ''
        capture = {'exit_status': result.returncode, 'stdout': result.stdout,
                   'stderr': result.stderr, 'calls': calls, 'github_output': output}
        self.assertNotIn(TOKEN, json.dumps(capture), 'publisher leaked the registry credential')
        artifact = ARTIFACTS / f'{self._testMethodName}-{self.sequence}.image.json'
        artifact.write_text(json.dumps(capture, indent=2) + '\n')
        self.assertEqual(result.returncode == 0, success, result.stderr)
        return calls, output

    def assert_no_registry(self, calls):
        self.assertFalse(any(call['argv'][0] in ('login', 'tag', 'push') for call in calls), calls)

    def assert_credentials_removed(self, calls):
        configs = {call['docker_config'] for call in calls if call['argv'][0] == 'login'}
        self.assertEqual(len(configs), 1, 'login needs exactly one isolated credential directory')
        for config in configs:
            self.assertTrue(config, 'login must have an isolated Docker config')
            self.assertFalse(Path(config).exists(), 'registry credentials survived publisher exit')
        self.assertEqual([call.get('stdin_present') for call in calls if call['argv'][0] == 'login'], [True])
        self.assertTrue(all('--password' not in call['argv'] for call in calls))

    def test_trusted_push_publishes_verified_archive_and_removes_credentials(self):
        for branch in ('beta', 'main'):
            with self.subTest(branch=branch):
                calls, output = self.run_publisher(True, GITHUB_REF='refs/heads/' + branch)
                operations = [call['argv'][0] for call in calls]
                self.assertLess(operations.index('load'), operations.index('login'))
                self.assertLess(operations.index('login'), operations.index('push'))
                load = next(call['argv'] for call in calls if call['argv'][0] == 'load')
                self.assertIn('--input', load)
                self.assertEqual(Path(load[load.index('--input') + 1]), self.archive)
                self.assertIn('image=' + DIGEST + '\n', output)
                self.assert_credentials_removed(calls)

    def test_pr_manual_and_other_branch_never_invoke_docker(self):
        for overrides in ({'GITHUB_EVENT_NAME': 'pull_request'},
                          {'GITHUB_EVENT_NAME': 'workflow_dispatch'},
                          {'GITHUB_REF': 'refs/heads/feature'}, {'GITHUB_REF': 'refs/tags/main'}):
            with self.subTest(overrides=overrides):
                calls, output = self.run_publisher(False, **overrides)
                self.assertEqual(calls, [])
                self.assertEqual(output, '')

    def test_invalid_source_revision_never_invoke_docker(self):
        for revision in ('a' * 39, 'g' * 40, SHA + '\n', '../unsafe'):
            with self.subTest(revision=revision):
                calls, output = self.run_publisher(False, GITHUB_SHA=revision)
                self.assertEqual(calls, [])
                self.assertEqual(output, '')

    def test_archive_checksum_tampering_prevents_load_and_login(self):
        self.archive.write_bytes(b'changed after scanning\n')
        calls, output = self.run_publisher(False)
        self.assertEqual(calls, [])
        self.assertEqual(output, '')

    def test_loaded_image_revision_mismatch_prevents_publication(self):
        calls, output = self.run_publisher(False, MOCK_REVISION='c' * 40)
        self.assertTrue(any(call['argv'][0] == 'load' for call in calls))
        self.assert_no_registry(calls)
        self.assertEqual(output, '')

    def test_invalid_registry_digest_cannot_become_release_output(self):
        for digest in ('ghcr.io/other/repo@sha256:' + 'b' * 64,
                       'ghcr.io/kanterlabs/helm@sha256:short', DIGEST + '\n' + DIGEST):
            with self.subTest(digest=digest):
                calls, output = self.run_publisher(False, MOCK_DIGEST=digest)
                self.assertEqual(output, '')
                self.assert_credentials_removed(calls)

    def test_failed_push_removes_transient_registry_credentials(self):
        calls, output = self.run_publisher(False, MOCK_PUSH_FAILURE='true')
        self.assertTrue(any(call['argv'][0] == 'push' for call in calls))
        self.assert_credentials_removed(calls)
        self.assertEqual(output, '')


if __name__ == '__main__':
    unittest.main(verbosity=2)
