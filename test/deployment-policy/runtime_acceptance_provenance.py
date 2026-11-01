#!/usr/bin/env python3
"""Verify retained CI OCI blobs and independently resolve checkout/source Git trees."""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import zipfile

# Retain the original accepted pair for callers that do not supply overrides.
RUNS = {
    'candidate': (41238074080, 'f045958d6c31e9ca900c4704d166b5e9edc6ed71', 'bbed78128e35fa74117c334329c0f996ed99d251'),
    'recovery': (41227731125, '690620c9b9b4a037cf5aa2447cf29d8441a090c9', '2ae94304beb445245d9c543558b0274c2a9dda33'),
}


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def capture(args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs)


def api(path):
    return json.loads(capture(['gh', 'api', 'repos/KanterLabs/helm/'+path]))


def source_tree(head, checkout):
    """Resolve identities through authenticated Git objects, not image labels."""
    head_commit = api('git/commits/'+head)
    checkout_commit = head_commit if checkout == head else api('git/commits/'+checkout)
    assert head_commit['sha'] == head and checkout_commit['sha'] == checkout, 'Git object identity mismatch'
    if checkout != head:
        assert head in [p['sha'] for p in checkout_commit['parents']], 'PR checkout does not parent source'
        assert checkout_commit['tree']['sha'] == head_commit['tree']['sha'], 'PR merge tree differs from head'
    return head_commit['tree']['sha']


def artifact_pair(listing, checkout):
    result = {}
    for kind in ['image', 'binary']:
        matching = [item for item in listing if item['name'] == f'helm-{kind}-{checkout}']
        assert len(matching) == 1, f'expected one {kind} artifact for actual checkout'
        item, = matching
        assert not item.get('expired', False), 'GitHub artifact has expired'
        assert re.fullmatch(r'sha256:[0-9a-f]{64}', item.get('digest', '')), 'missing authenticated artifact ZIP digest'
        result[kind] = item
    return result


def oci_archive(archive):
    """Validate every OCI blob and all referenced descriptor sizes and digests."""
    with tarfile.open(archive) as tar:
        members = tar.getmembers()
        assert len({member.name for member in members}) == len(members), 'duplicate OCI archive path'
        for member in members:
            if member.name.startswith('blobs/') and not member.isdir():
                assert member.isfile(), 'OCI blob must be a regular file'
                match = re.fullmatch(r'blobs/sha256/([0-9a-f]{64})', member.name)
                assert match and sha(tar.extractfile(member).read()) == match[1], 'OCI blob digest mismatch'

        def blob(descriptor):
            digest = descriptor['digest']
            assert re.fullmatch(r'sha256:[0-9a-f]{64}', digest), 'unsupported OCI digest'
            raw = tar.extractfile('blobs/sha256/'+digest[7:]).read()
            assert 'sha256:'+sha(raw) == digest and len(raw) == descriptor['size'], 'OCI descriptor mismatch'
            return raw

        index = json.load(tar.extractfile('index.json'))
        descriptor, = index['manifests']
        manifest = json.loads(blob(descriptor))
        config_descriptor = manifest['config']
        json.loads(blob(config_descriptor))
        for layer in manifest['layers']:
            blob(layer)
    return descriptor['digest'], config_descriptor['digest']


def container_log_binding(log, checkout, config_digest, zip_digest):
    # Both checkout@v7 shapes emit the full git-log SHA. Branch pushes omit the
    # PR-only "HEAD is now at ... Merge ..." message, which is not our authority.
    clean = re.sub(r'\x1b\[[0-9;]*m', '', log)
    lines = [re.sub(r'^\d{4}-\d\d-\d\dT\S+Z ', '', line).strip() for line in clean.splitlines()]
    checkout_positions = [i for i, line in enumerate(lines[:-1])
                          if re.search(r'\bgit log -1 --format=%H$', line) and lines[i+1] == checkout]
    assert checkout_positions, 'container checkout full SHA not authenticated by git log'
    builds = [i for i, line in enumerate(lines) if re.search(r'\bdocker build\b', line)]
    writes = [i for i, line in enumerate(lines) if 'writing image '+config_digest+' ' in line]
    uploads = [i for i, line in enumerate(lines) if line == 'Uploading artifact: helm-image-'+checkout+'.zip']
    digests = [i for i, line in enumerate(lines) if line == 'SHA256 digest of uploaded artifact is '+zip_digest[7:]]
    assert any(c < b < w < u < d for c in checkout_positions for b in builds for w in writes for u in uploads for d in digests), 'container checkout/build/upload chain mismatch'
    assert any('docker save --output' in line for line in lines), 'container image save command missing'


def vcs_revision(info):
    matches = re.findall(r'^\s*build\s+vcs\.revision=([0-9a-f]{40})\s*$', info, re.MULTILINE)
    assert len(matches) <= 1, 'ambiguous binary VCS revision'
    return matches[0] if matches else None


def verify(root, go, runs=None):
    runs = RUNS if runs is None else runs
    assert set(runs) == {'candidate', 'recovery'}, 'candidate and recovery inputs required'
    for run_id, head, checkout in runs.values():
        assert isinstance(run_id, int) and run_id > 0, 'run ID must be positive'
        assert re.fullmatch(r'[0-9a-f]{40}', head) and re.fullmatch(r'[0-9a-f]{40}', checkout), 'full Git SHA required'
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    # A failed revalidation must not leave an earlier success receipt behind.
    (root/'provenance.json').unlink(missing_ok=True)
    result = {'kind': 'tc336-github-ci-oci-provenance', 'artifacts': {}}
    for role, (run_id, head, checkout) in runs.items():
        run = api(f'actions/runs/{run_id}')
        assert run['head_sha'] == head and run['status'] == 'completed' and run['conclusion'] == 'success', 'CI run is not successful for expected source'
        tree = source_tree(head, checkout)
        listing = api(f'actions/runs/{run_id}/artifacts')['artifacts']
        artifacts = artifact_pair(listing, checkout)
        (root/role).mkdir(mode=0o700, parents=True, exist_ok=True)
        for kind, item in artifacts.items():
            downloaded = root/role/(kind+'-artifact.zip')
            if not downloaded.exists():
                with downloaded.open('wb') as stream:
                    subprocess.run(['gh', 'api', f"repos/KanterLabs/helm/actions/artifacts/{item['id']}/zip"], stdout=stream, check=True)
            with downloaded.open('rb') as stream:
                assert 'sha256:'+hashlib.file_digest(stream, 'sha256').hexdigest() == item['digest'], 'GitHub artifact ZIP digest mismatch'
        folder = root/role/f'helm-image-{checkout}'
        archive = folder/f'helm-{checkout}.tar.gz'
        want, name = (folder/(archive.name+'.sha256')).read_text().split()
        assert name == archive.name and sha(archive.read_bytes()) == want, 'image archive checksum mismatch'
        with zipfile.ZipFile(root/role/'image-artifact.zip') as zipped:
            assert sha(zipped.read(archive.name)) == want, 'downloaded image archive differs from authenticated ZIP'
        image, config_digest = oci_archive(archive)
        checked = root/role/f'helm-binary-{checkout}'/'helm'
        with zipfile.ZipFile(root/role/'binary-artifact.zip') as zipped:
            assert sha(zipped.read('helm')) == sha(checked.read_bytes()), 'checks executable differs from authenticated ZIP'
        jobs = api(f'actions/runs/{run_id}/jobs')['jobs']
        container_jobs = [job for job in jobs if job['name'] == 'container']
        assert len(container_jobs) == 1, 'expected one CI container job'
        container_job, = container_jobs
        assert container_job['conclusion'] == 'success', 'CI container job failed'
        ci_log = capture(['gh', 'api', f"repos/KanterLabs/helm/actions/jobs/{container_job['id']}/logs"])
        container_log_binding(ci_log, checkout, config_digest, artifacts['image']['digest'])
        container = capture(['docker', 'create', image]).strip()
        extracted = root/role/'image-helm'
        try:
            subprocess.run(['docker', 'cp', container+':/usr/local/bin/helm', str(extracted)], check=True, stdout=subprocess.DEVNULL)
        finally:
            subprocess.run(['docker', 'rm', '-v', container], check=True, stdout=subprocess.DEVNULL)
        env = {**os.environ, 'GOTOOLCHAIN': 'local'}
        checked_info = capture([go, 'version', '-m', str(checked)], env=env)
        image_info = capture([go, 'version', '-m', str(extracted)], env=env)
        assert vcs_revision(checked_info) == checkout, 'checks binary VCS revision differs from checkout'
        image_revision = vcs_revision(image_info)
        assert image_revision is None or image_revision == checkout, 'image binary VCS revision differs from checkout'
        result['artifacts'][role] = {
            'run_id': run_id, 'run_url': run['html_url'], 'head_sha': head, 'actual_checkout_sha': checkout,
            'head_and_checkout_tree': tree, 'archive': archive.name,
            'archive_sha256': want, 'manifest_digest': image, 'config_digest': config_digest,
            'registry_image': 'ghcr.io/kanterlabs/helm@'+image,
            'image_helm_sha256': sha(extracted.read_bytes()), 'checks_helm_sha256': sha(checked.read_bytes()),
            'checks_embedded_vcs_revision': checkout, 'checks_embedded_vcs_modified': 'vcs.modified=true' in checked_info,
            'image_embedded_vcs_present': image_revision is not None,
            'source_binding': 'GitHub authenticated successful CI run checkout/build/upload chain; independent Git object tree equality',
            'oci_blob_hashes_verified': True, 'github_zip_digests_verified': True, 'ci_container_log_sha256': sha(ci_log.encode()),
            'artifact_metadata': {kind: {'id': value['id'], 'digest': value['digest'], 'name': value['name']} for kind, value in artifacts.items()}}
    (root/'provenance.json').write_text(json.dumps(result, indent=2)+'\n')
    print(json.dumps(result, indent=2))


def positive_run_id(value):
    if not re.fullmatch(r'[0-9]+', value) or int(value) < 1:
        raise argparse.ArgumentTypeError('run ID must be a positive decimal integer')
    return int(value)


def git_sha(value):
    if not re.fullmatch(r'[0-9a-f]{40}', value):
        raise argparse.ArgumentTypeError('Git SHA must contain 40 lowercase hexadecimal characters')
    return value


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--go', default='/home/shane/.cache/helm-go-1.25.14/go/bin/go')
    for role, (run_id, head, checkout) in RUNS.items():
        parser.add_argument('--'+role+'-run-id', type=positive_run_id, default=run_id)
        parser.add_argument('--'+role+'-source-sha', type=git_sha, default=head)
        parser.add_argument('--'+role+'-checkout-sha', type=git_sha, default=checkout)
    options = parser.parse_args()
    selected = {role: (getattr(options, role+'_run_id'), getattr(options, role+'_source_sha'), getattr(options, role+'_checkout_sha')) for role in RUNS}
    verify(options.root, options.go, selected)
