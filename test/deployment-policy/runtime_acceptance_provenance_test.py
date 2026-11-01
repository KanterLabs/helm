#!/usr/bin/env python3
"""Failure-first isolation checks for TC336_PROVENANCE_FAILURES.md."""
from __future__ import annotations
import contextlib
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile

MODULE = Path(__file__).resolve().with_name('runtime_acceptance_provenance.py')
spec = importlib.util.spec_from_file_location('provenance', MODULE)
provenance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(provenance)


def digest(raw):
    return 'sha256:'+hashlib.sha256(raw).hexdigest()


class Evidence:
    """Tiny retained artifact pair; fake only external process/API boundaries."""
    def __init__(self, root):
        self.root = root
        self.runs = {'candidate': (101, 'a'*40, 'a'*40), 'recovery': (102, 'b'*40, 'c'*40)}
        self.responses = {}
        self.logs = {}
        self.vcs = {}
        self.blobs = {}
        self.api_calls = []
        for role, (run_id, head, checkout) in self.runs.items():
            folder = root/role
            folder.mkdir()
            image_folder = folder/f'helm-image-{checkout}'
            image_folder.mkdir()
            binary_folder = folder/f'helm-binary-{checkout}'
            binary_folder.mkdir()
            checked = b'synthetic checks binary '+checkout.encode()
            (binary_folder/'helm').write_bytes(checked)
            self.vcs[str(binary_folder/'helm')] = '\tbuild\tvcs.revision='+checkout+'\n'
            self.vcs[str(folder/'image-helm')] = 'synthetic image build without VCS\n'
            layer = b'synthetic compressed layer'
            config = b'{"architecture":"amd64","os":"linux"}'
            descriptor = lambda raw: {'digest': digest(raw), 'size': len(raw)}
            manifest = json.dumps({'schemaVersion': 2, 'config': descriptor(config), 'layers': [descriptor(layer)]}).encode()
            index = json.dumps({'schemaVersion': 2, 'manifests': [descriptor(manifest)]}).encode()
            self.blobs[role] = {'index.json': index, 'oci-layout': b'{"imageLayoutVersion":"1.0.0"}',
                               'blobs/sha256/'+digest(manifest)[7:]: manifest,
                               'blobs/sha256/'+digest(config)[7:]: config,
                               'blobs/sha256/'+digest(layer)[7:]: layer}
            image_id, binary_id = run_id*10, run_id*10+1
            items = [{'id': image_id, 'name': f'helm-image-{checkout}', 'expired': False},
                     {'id': binary_id, 'name': f'helm-binary-{checkout}', 'expired': False}]
            self.responses[f'actions/runs/{run_id}'] = {'id': run_id, 'head_sha': head, 'status': 'completed',
                                                      'conclusion': 'success', 'html_url': f'https://example.test/run/{run_id}'}
            self.responses[f'actions/runs/{run_id}/artifacts'] = {'artifacts': items}
            self.responses[f'git/commits/{head}'] = {'sha': head, 'tree': {'sha': 'd'*40}, 'parents': []}
            if checkout != head:
                self.responses[f'git/commits/{checkout}'] = {'sha': checkout, 'tree': {'sha': 'd'*40}, 'parents': [{'sha': head}]}
            self.responses[f'actions/runs/{run_id}/jobs'] = {'jobs': [{'id': run_id*100, 'run_id': run_id,
                                                                    'name': 'container', 'conclusion': 'success'}]}
            self.write_archive(role)
            with zipfile.ZipFile(folder/'binary-artifact.zip', 'w') as archive:
                archive.writestr('helm', checked)
            items[1]['digest'] = digest((folder/'binary-artifact.zip').read_bytes())
            config_digest = digest(config)
            checkout_log = '[command]/usr/bin/git checkout --progress --force -B beta refs/remotes/origin/beta\n'
            if checkout != head:
                checkout_log = '[command]/usr/bin/git checkout --progress --force refs/remotes/pull/58/merge\n'
                checkout_log += f'HEAD is now at {checkout[:7]} Merge {head} into '+('e'*40)+'\n'
            self.logs[run_id*100] = (checkout_log+'[command]/usr/bin/git log -1 --format=%H\n'+checkout+'\n'
                'docker build --pull --tag "helm:${GITHUB_SHA}" .\n'
                'docker save --output "$image_tar" "helm:${GITHUB_SHA}"\n'
                f'#37 writing image {config_digest} done\n'
                f'Uploading artifact: helm-image-{checkout}.zip\n'
                f'SHA256 digest of uploaded artifact is {items[0]["digest"][7:]}\n')

    def write_archive(self, role):
        run_id, _, checkout = self.runs[role]
        folder = self.root/role/f'helm-image-{checkout}'
        archive = folder/f'helm-{checkout}.tar.gz'
        with tarfile.open(archive, 'w:gz') as tar:
            for name, raw in self.blobs[role].items():
                info = tarfile.TarInfo(name)
                info.size = len(raw)
                tar.addfile(info, io.BytesIO(raw))
        checksum = digest(archive.read_bytes())[7:]
        (folder/(archive.name+'.sha256')).write_text(checksum+'  '+archive.name+'\n')
        with zipfile.ZipFile(self.root/role/'image-artifact.zip', 'w') as zipped:
            zipped.writestr(archive.name, archive.read_bytes())
        items = self.responses[f'actions/runs/{run_id}/artifacts']['artifacts']
        old_digest = items[0].get('digest')
        items[0]['digest'] = digest((self.root/role/'image-artifact.zip').read_bytes())
        if old_digest and run_id*100 in self.logs:
            self.logs[run_id*100] = self.logs[run_id*100].replace(old_digest[7:], items[0]['digest'][7:])

    def api(self, path):
        self.api_calls.append(path)
        return self.responses[path]

    def capture(self, args, **kwargs):
        if args[:2] == ['docker', 'create']:
            return 'synthetic-container\n'
        if args[0] == 'synthetic-go':
            return self.vcs[args[-1]]
        if args[:2] == ['gh', 'api']:
            job = int(args[-1].split('/')[-2])
            # Real logs carry ISO timestamps and terminal color sequences.
            return ''.join('2026-11-01T00:00:00.0000000Z \x1b[36;1m'+line+'\x1b[0m\n' for line in self.logs[job].splitlines())
        raise AssertionError('unexpected capture boundary '+repr(args))

    def run(self, args, **kwargs):
        if args[:2] == ['docker', 'cp']:
            Path(args[-1]).write_bytes(b'synthetic image binary')
        elif args[:2] != ['docker', 'rm']:
            raise AssertionError('unexpected process boundary '+repr(args))
        return subprocess.CompletedProcess(args, 0)

    def verify(self):
        with patch.object(provenance, 'api', self.api), patch.object(provenance, 'capture', self.capture), \
                patch.object(provenance.subprocess, 'run', self.run), contextlib.redirect_stdout(io.StringIO()):
            provenance.verify(self.root, 'synthetic-go', self.runs)
        return json.loads((self.root/'provenance.json').read_text())


class ProvenanceBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.evidence = Evidence(self.root)

    def rejects(self):
        with self.assertRaises((AssertionError, ValueError, KeyError)):
            self.evidence.verify()
        self.assertFalse((self.root/'provenance.json').exists(), 'failure must not emit a success receipt')

    def test_direct_push_and_retained_pr_both_verify(self):
        result = self.evidence.verify()
        for role, (run_id, head, checkout) in self.evidence.runs.items():
            receipt = result['artifacts'][role]
            self.assertEqual((receipt['run_id'], receipt['head_sha'], receipt['actual_checkout_sha']), (run_id, head, checkout))
            self.assertTrue(receipt['oci_blob_hashes_verified'])
            self.assertTrue(receipt['github_zip_digests_verified'])
            self.assertIn('git/commits/'+head, self.evidence.api_calls)
            self.assertIn('git/commits/'+checkout, self.evidence.api_calls)

    def test_run_must_complete_successfully_with_expected_head(self):
        for key, value in [('conclusion', 'failure'), ('status', 'in_progress'), ('head_sha', 'f'*40)]:
            with self.subTest(key=key):
                saved = self.evidence.responses['actions/runs/101'][key]
                self.evidence.responses['actions/runs/101'][key] = value
                self.rejects()
                self.evidence.responses['actions/runs/101'][key] = saved

    def test_git_object_must_match_requested_sha(self):
        self.evidence.responses['git/commits/'+'a'*40]['sha'] = 'f'*40
        self.rejects()

    def test_pr_checkout_requires_source_parent_and_equal_tree(self):
        commit = self.evidence.responses['git/commits/'+'c'*40]
        commit['parents'] = []
        self.rejects()
        commit['parents'] = [{'sha': 'b'*40}]
        commit['tree']['sha'] = 'f'*40
        self.rejects()

    def test_artifacts_must_be_unique_current_and_named_for_checkout(self):
        items = self.evidence.responses['actions/runs/101/artifacts']['artifacts']
        original = [dict(item) for item in items]
        for change in ['missing', 'duplicate', 'expired', 'wrong-checkout']:
            with self.subTest(change=change):
                items[:] = [dict(item) for item in original]
                if change == 'missing': items.pop()
                if change == 'duplicate': items.append(dict(items[0]))
                if change == 'expired': items[0]['expired'] = True
                if change == 'wrong-checkout': items[0]['name'] = 'helm-image-'+'f'*40
                self.rejects()

    def test_both_downloaded_zip_digests_are_authenticated(self):
        for kind in ['image', 'binary']:
            with self.subTest(kind=kind):
                path = self.root/'candidate'/(kind+'-artifact.zip')
                raw = path.read_bytes()
                path.write_bytes(raw+b'tamper')
                self.rejects()
                path.write_bytes(raw)

    def test_local_extractions_must_match_authenticated_zip(self):
        checkout = 'a'*40
        binary = self.root/'candidate'/f'helm-binary-{checkout}'/'helm'
        original = binary.read_bytes()
        binary.write_bytes(b'changed local binary')
        self.rejects()
        binary.write_bytes(original)
        image = self.root/'candidate'/f'helm-image-{checkout}'/f'helm-{checkout}.tar.gz'
        image.write_bytes(image.read_bytes()+b'changed archive')
        self.rejects()

    def test_each_oci_blob_is_verified_even_if_unreferenced(self):
        blobs = self.evidence.blobs['candidate']
        for name in list(blobs):
            if not name.startswith('blobs/'):
                continue
            with self.subTest(blob=name):
                original = blobs[name]
                blobs[name] = original+b'tamper'
                self.evidence.write_archive('candidate')
                self.rejects()
                blobs[name] = original
        blobs['blobs/sha256/'+'f'*64] = b'unreferenced corrupted blob'
        self.evidence.write_archive('candidate')
        self.rejects()

    def test_manifest_and_config_descriptor_sizes_are_verified(self):
        blobs = self.evidence.blobs['candidate']
        index = json.loads(blobs['index.json'])
        index['manifests'][0]['size'] += 1
        blobs['index.json'] = json.dumps(index).encode()
        self.evidence.write_archive('candidate')
        self.rejects()
        index['manifests'][0]['size'] -= 1
        entry = index['manifests'][0]
        manifest_name = 'blobs/sha256/'+entry['digest'][7:]
        manifest = json.loads(blobs.pop(manifest_name))
        manifest['config']['size'] += 1
        raw = json.dumps(manifest).encode()
        entry.update(digest=digest(raw), size=len(raw))
        blobs['blobs/sha256/'+digest(raw)[7:]] = raw
        blobs['index.json'] = json.dumps(index).encode()
        self.evidence.write_archive('candidate')
        self.rejects()

    def test_checks_vcs_and_present_image_vcs_must_match_checkout(self):
        for name in ['helm-binary-'+'a'*40+'/helm', 'image-helm']:
            path = str(self.root/'candidate'/name)
            original = self.evidence.vcs[path]
            self.evidence.vcs[path] = '\tbuild\tvcs.revision='+'f'*40+'\n'
            self.rejects()
            self.evidence.vcs[path] = original

    def test_container_job_must_be_successful(self):
        self.evidence.responses['actions/runs/101/jobs']['jobs'][0]['conclusion'] = 'failure'
        self.rejects()

    def test_authenticated_checkout_build_and_upload_links_are_required(self):
        original = self.evidence.logs[10100]
        changes = {
            'checkout': original.replace('git log -1 --format=%H\n'+'a'*40, 'git log -1 --format=%H\n'+'f'*40),
            'short-checkout': original.replace('a'*40+'\n', 'a'*7+'\n', 1),
            'build': original.replace('docker build', 'echo build'),
            'config': original.replace('writing image sha256:', 'unrelated label sha256:'),
            'save': original.replace('docker save --output', 'echo save'),
            'upload-name': original.replace('Uploading artifact: helm-image-', 'Uploading artifact: other-image-'),
            'upload-digest': original.replace('SHA256 digest of uploaded artifact is ', 'unrelated checksum '),
        }
        for name, changed in changes.items():
            with self.subTest(link=name):
                self.evidence.logs[10100] = changed
                self.rejects()
        self.evidence.logs[10100] = original


class ProvenanceCLITests(unittest.TestCase):
    def test_explicit_role_inputs_are_documented(self):
        result = subprocess.run(['python3', str(MODULE), '--help'], text=True, capture_output=True)
        self.assertEqual(result.returncode, 0)
        for role in ['candidate', 'recovery']:
            for field in ['run-id', 'source-sha', 'checkout-sha']:
                self.assertIn('--'+role+'-'+field, result.stdout)

    def test_malformed_inputs_fail_before_network(self):
        for flag, value in [('candidate-run-id', '0'), ('candidate-source-sha', 'abc'), ('recovery-checkout-sha', 'x'*40)]:
            with self.subTest(flag=flag):
                result = subprocess.run(['python3', str(MODULE), '--root', '/nonexistent', '--'+flag, value], text=True, capture_output=True)
                self.assertEqual(result.returncode, 2)
                self.assertNotIn('Traceback', result.stderr)

    def test_historical_defaults_are_retained(self):
        self.assertEqual(provenance.RUNS['candidate'], (41238074080, 'f045958d6c31e9ca900c4704d166b5e9edc6ed71', 'bbed78128e35fa74117c334329c0f996ed99d251'))
        self.assertEqual(provenance.RUNS['recovery'], (41227731125, '690620c9b9b4a037cf5aa2447cf29d8441a090c9', '2ae94304beb445245d9c543558b0274c2a9dda33'))


if __name__ == '__main__':
    unittest.main(verbosity=2)
