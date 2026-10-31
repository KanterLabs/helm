#!/usr/bin/env python3
"""Verify retained CI OCI blobs and independently resolve merge/head Git trees."""
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


def verify(root, go):
    result = {'kind': 'tc336-github-ci-oci-provenance', 'artifacts': {}}
    for role, (run_id, head, merge) in RUNS.items():
        run = api(f'actions/runs/{run_id}')
        assert run['head_sha'] == head and run['conclusion'] == 'success'
        listing = api(f'actions/runs/{run_id}/artifacts')['artifacts']
        artifacts = {kind: next(a for a in listing if a['name'] == f'helm-{kind}-{merge}') for kind in ['image', 'binary']}
        for kind, item in artifacts.items():
            downloaded = root/role/(kind+'-artifact.zip')
            if not downloaded.exists():
                with downloaded.open('wb') as stream:
                    subprocess.run(['gh', 'api', f"repos/KanterLabs/helm/actions/artifacts/{item['id']}/zip"], stdout=stream, check=True)
            with downloaded.open('rb') as stream:
                assert 'sha256:'+hashlib.file_digest(stream, 'sha256').hexdigest() == item['digest'], 'GitHub artifact ZIP digest mismatch'
        merge_commit, head_commit = api('git/commits/'+merge), api('git/commits/'+head)
        assert head in [p['sha'] for p in merge_commit['parents']]
        assert merge_commit['tree']['sha'] == head_commit['tree']['sha'], 'PR merge tree differs from head'
        folder = root/role/f'helm-image-{merge}'
        archive = folder/f'helm-{merge}.tar.gz'
        want, name = (folder/(archive.name+'.sha256')).read_text().split()
        assert name == archive.name and sha(archive.read_bytes()) == want
        with zipfile.ZipFile(root/role/'image-artifact.zip') as zipped:
            assert sha(zipped.read(archive.name)) == want, 'downloaded image archive differs from authenticated ZIP'
        with tarfile.open(archive) as tar:
            index = json.load(tar.extractfile('index.json'))
            descriptor, = index['manifests']
            manifest_raw = tar.extractfile('blobs/sha256/'+descriptor['digest'].split(':')[1]).read()
            assert 'sha256:'+sha(manifest_raw) == descriptor['digest']
            manifest = json.loads(manifest_raw)
            config_descriptor = manifest['config']
            config_raw = tar.extractfile('blobs/sha256/'+config_descriptor['digest'].split(':')[1]).read()
            assert 'sha256:'+sha(config_raw) == config_descriptor['digest']
            config = json.loads(config_raw)
            # Validate every referenced compressed OCI blob before Docker load.
            for layer in manifest['layers']:
                raw = tar.extractfile('blobs/sha256/'+layer['digest'].split(':')[1]).read()
                assert 'sha256:'+sha(raw) == layer['digest'] and len(raw) == layer['size']
        image = descriptor['digest']
        container = capture(['docker', 'create', image]).strip()
        extracted = root/role/'image-helm'
        try:
            subprocess.run(['docker', 'cp', container+':/usr/local/bin/helm', str(extracted)], check=True, stdout=subprocess.DEVNULL)
        finally:
            subprocess.run(['docker', 'rm', '-v', container], check=True, stdout=subprocess.DEVNULL)
        checked = root/role/f'helm-binary-{merge}'/'helm'
        with zipfile.ZipFile(root/role/'binary-artifact.zip') as zipped:
            assert sha(zipped.read('helm')) == sha(checked.read_bytes()), 'checks executable differs from authenticated ZIP'
        jobs = api(f'actions/runs/{run_id}/jobs')['jobs']
        container_job = next(job for job in jobs if job['name'] == 'container')
        assert container_job['conclusion'] == 'success'
        ci_log = capture(['gh', 'api', f"repos/KanterLabs/helm/actions/jobs/{container_job['id']}/logs"])
        assert 'HEAD is now at '+merge[:7] in ci_log and 'Merge '+head in ci_log
        assert 'writing image '+config_descriptor['digest'] in ci_log
        assert 'docker save --output' in ci_log and 'Uploading artifact: helm-image-'+merge+'.zip' in ci_log
        assert artifacts['image']['digest'].split(':')[1] in ci_log
        env = {**os.environ, 'GOTOOLCHAIN': 'local'}
        checked_info = capture([go, 'version', '-m', str(checked)], env=env)
        image_info = capture([go, 'version', '-m', str(extracted)], env=env)
        assert 'vcs.revision='+merge in checked_info
        result['artifacts'][role] = {
            'run_id': run_id, 'run_url': run['html_url'], 'head_sha': head, 'actual_checkout_sha': merge,
            'head_and_checkout_tree': head_commit['tree']['sha'], 'archive': archive.name,
            'archive_sha256': want, 'manifest_digest': image, 'config_digest': config_descriptor['digest'],
            'registry_image': 'ghcr.io/kanterlabs/helm@'+image,
            'image_helm_sha256': sha(extracted.read_bytes()), 'checks_helm_sha256': sha(checked.read_bytes()),
            'checks_embedded_vcs_revision': merge, 'checks_embedded_vcs_modified': 'vcs.modified=true' in checked_info,
            'image_embedded_vcs_present': 'vcs.revision=' in image_info,
            'source_binding': 'GitHub authenticated successful CI run checkout/build/upload chain; independent Git object tree equality',
            'oci_blob_hashes_verified': True, 'github_zip_digests_verified': True, 'ci_container_log_sha256': sha(ci_log.encode()),
            'artifact_metadata': {kind: {'id': value['id'], 'digest': value['digest'], 'name': value['name']} for kind, value in artifacts.items()}}
    (root/'provenance.json').write_text(json.dumps(result, indent=2)+'\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--go', default='/home/shane/.cache/helm-go-1.25.14/go/bin/go')
    options = parser.parse_args()
    verify(options.root, options.go)
