"""Publish a version only after its protected-main merge is live in production."""
from pathlib import Path
import tempfile

from release_prepare import manifest_at, validate_branch
from release_support import SHA, ReleaseError, bounded_write, version_tuple


def finish(runtime, version):
    version_tuple(version)
    branch = 'release/' + version
    runtime.fetch()
    prs = runtime.prs(branch)
    if len(prs) != 1 or prs[0].get('state') != 'MERGED':
        raise ReleaseError('Exactly one merged release PR into main is required; preparation never merges it')
    pr = prs[0]
    merged = (pr.get('mergeCommit') or {}).get('oid', '')
    head = pr.get('headRefOid', '')
    if not SHA.fullmatch(merged) or not SHA.fullmatch(head):
        raise ReleaseError('Release PR has invalid head/merge revisions')
    if runtime.ref('origin', 'main') != merged:
        raise ReleaseError('GitHub main is not this release merge revision; do not tag another deployment')
    if runtime.git('diff', '--name-only', head, merged):
        raise ReleaseError('Merge tree differs from the prepared release; inspect before publication')
    manifest = manifest_at(runtime, merged, version)
    if any(not SHA.fullmatch(manifest.get(key, '')) for key in ['beta_sha', 'main_sha']):
        raise ReleaseError('Release manifest contains invalid source revisions')
    _, notes = validate_branch(runtime, merged, version, manifest['beta_sha'], manifest['main_sha'])
    evidence = runtime.deployed('production', merged)
    canonical = runtime.ref('gitea', 'main')
    if not canonical or not runtime.ancestor(canonical, merged):
        raise ReleaseError('Canonical main diverged; reconcile without force-pushing before publication')
    # Check all collisions before synchronizing or creating any tags/releases.
    for remote in ['gitea', 'origin']:
        existing = runtime.ref(remote, version, tag=True)
        if existing and existing != merged:
            raise ReleaseError(f'{remote} tag {version} points elsewhere; refusing overwrite')
    local_tag = runtime.git('rev-parse', '--verify', f'refs/tags/{version}^{{commit}}', allow_failure=True)
    if local_tag and local_tag != merged:
        raise ReleaseError('Local version tag points elsewhere; refusing overwrite')
    body = (notes + '\n\n## Verified production artifact\n\n'
            f'- Revision: `{merged}`\n- Image: `{evidence["image"]}`\n'
            f'- Authorization acceptance: `{evidence["authorization"]["bundle_sha256"]}`\n'
            f'- Authorization floor: `{evidence["authorization"]["minimum"]}` '
            f'(`{evidence["authorization"]["floor_sha256"]}`)\n'
            f'- Recovery image: `{evidence["authorization"]["recovery"]["image"]}`\n'
            f'- Recovery revision: `{evidence["authorization"]["recovery"]["sha"]}`\n'
            f'- Stop observer: `{evidence["authorization"]["stop_observer_sha256"]}`\n'
            f'- Beta acceptance: `{manifest["beta_authorization"]["bundle_sha256"]}`\n'
            f'- CI and deployment: {evidence["url"]}\n')
    if len(body.encode()) > 20_000:
        raise ReleaseError('Published release notes exceed 20,000 bytes')
    github = runtime.gh('release', 'view', version, '--json', 'tagName,body,isDraft,isPrerelease',
                        json_result=True, allow_failure=True)
    gitea = runtime.gitea('/releases/tags/' + version, missing_ok=True)
    for host, release in [('GitHub', github), ('Gitea', gitea)]:
        if release and (release.get('body', '').strip() != body.strip()
                        or release.get('isDraft', release.get('draft', False))
                        or release.get('isPrerelease', release.get('prerelease', False))):
            raise ReleaseError(f'{host} release already exists with different notes/status; refusing overwrite')
    if runtime.ref('origin', 'main') != merged:
        raise ReleaseError('Main advanced during verification; no release was published')
    runtime.ready('production', merged)
    if canonical != merged:
        runtime.git('push', 'gitea', f'{merged}:refs/heads/main')
    if not local_tag:
        runtime.git('tag', '-a', version, merged, '-m', 'Helm ' + version)
    for remote in ['gitea', 'origin']:
        if runtime.ref(remote, version, tag=True) != merged:
            runtime.git('push', remote, f'refs/tags/{version}:refs/tags/{version}')
    if not gitea:
        runtime.gitea('/releases', body={'tag_name': version, 'target_commitish': merged,
            'name': 'Helm ' + version, 'body': body, 'draft': False, 'prerelease': False})
    if not github:
        with tempfile.TemporaryDirectory(prefix='helm-release-notes-') as directory:
            path = Path(directory) / 'notes.md'
            bounded_write(path, body)
            runtime.gh('release', 'create', version, '--verify-tag', '--title', 'Helm ' + version,
                       '--notes-file', str(path))
    print(f'Published Helm {version} at verified production revision {merged}.')
    print('https://github.com/KanterLabs/helm/releases/tag/' + version)
    print('https://gitea.home.shanekanterman.dev/KanterLabs/helm/releases/tag/' + version)
    print('Gitea and GitHub main/tags are synchronized. No database was copied or restored.')
