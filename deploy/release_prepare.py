"""Prepare tested beta plus version metadata for a protected main PR."""
import json
from pathlib import Path
import tempfile

from release_support import FILE_LIMIT, ReleaseError, bounded_write, version_tuple

MANIFEST = '.helm-release.json'


def metadata_path(path):
    return path in ['VERSION', MANIFEST] or (
        path.startswith('docs/releases/') and path.endswith('.md') and '..' not in path.split('/'))


def manifest_at(runtime, sha, version):
    raw = runtime.git('show', f'{sha}:{MANIFEST}', allow_failure=True)
    if raw is None or len(raw.encode()) > FILE_LIMIT:
        raise ReleaseError('Existing release branch has no bounded release manifest; inspect it manually')
    try:
        value = json.loads(raw)
    except ValueError:
        raise ReleaseError('Release manifest is invalid JSON') from None
    if value.get('format') != 1 or value.get('version') != version:
        raise ReleaseError('Release manifest version does not match')
    if value.get('notes') != f'docs/releases/{version}.md':
        raise ReleaseError('Release manifest notes path does not match')
    return value


def validate_branch(runtime, sha, version, beta, main):
    manifest = manifest_at(runtime, sha, version)
    if manifest.get('beta_sha') != beta or manifest.get('main_sha') != main:
        raise ReleaseError('Prepared release targets different beta/main revisions; inspect before re-preparing')
    if not runtime.ancestor(beta, sha) or not runtime.ancestor(main, sha):
        raise ReleaseError('Release branch does not contain its recorded beta and main commits')
    changed = runtime.git('diff', '--name-only', beta, sha).splitlines()
    if any(not metadata_path(path) for path in changed):
        raise ReleaseError('Release branch changes application code beyond tested beta; test it on beta first')
    if runtime.git('show', sha + ':VERSION') != version[1:]:
        raise ReleaseError('VERSION and release manifest disagree')
    notes = runtime.git('show', f'{sha}:{manifest["notes"]}')
    if not notes or len(notes.encode()) > FILE_LIMIT:
        raise ReleaseError('Release notes are missing or oversized')
    return manifest, notes


def available_version(runtime, version):
    requested = version_tuple(version)
    for remote in ['gitea', 'origin']:
        output = runtime.git('ls-remote', '--tags', '--refs', remote, 'refs/tags/v*')
        for line in output.splitlines():
            name = line.split()[1].removeprefix('refs/tags/')
            try:
                existing = version_tuple(name)
            except ReleaseError:
                continue
            if existing >= requested:
                raise ReleaseError(f'{remote} already has {name}; choose a newer unused stable version')


def release_notes(runtime, beta, main, version, notes_file):
    if notes_file:
        path = Path(notes_file)
        if not path.is_file() or path.stat().st_size > 12_000:
            raise ReleaseError('Notes file must exist and be at most 12,000 bytes')
        body = path.read_text().strip()
    else:
        raw = runtime.git('show', beta + ':docs/releases/unreleased.md', allow_failure=True)
        if not raw or len(raw.encode()) > 12_000:
            raise ReleaseError('Add bounded beta release notes or pass --notes FILE')
        # Drop the old Unreleased heading and stale "latest release" preamble.
        index = raw.find('\n## ')
        if index < 0:
            raise ReleaseError('Unreleased notes need a populated ## section, or pass --notes FILE')
        body = raw[index:].strip()
    if not body or 'No changes recorded yet.' in body:
        raise ReleaseError('Write release notes before preparing a release')
    schema = runtime.git('diff', '--name-only', main, beta, '--', 'internal/db').splitlines()
    compatibility = ('This release changes the schema or migration engine. Populated-data upgrade '
                     'and retained-image rollback compatibility must be verified before merging.'
                     if schema else 'No schema or migration-engine changes relative to the recorded main revision.')
    notes = (f'# Helm {version}\n\n{body}\n\n## Deployment and compatibility\n\n'
             f'{compatibility}\n\nProduction keeps its own database, runtime configuration, and credentials. '
             'Beta test data is never copied to production. Deployment changes the application image; '
             'rollback does not restore an older database.\n')
    return notes, schema


def prepare(runtime, version, *, dry_run=False, notes_file=None):
    available_version(runtime, version)
    beta, main = runtime.pair('beta'), runtime.pair('main')
    runtime.fetch()
    beta_evidence = runtime.deployed('beta', beta)
    runtime.ready('production', main)
    branch = 'release/' + version
    notes, schema = release_notes(runtime, beta, main, version, notes_file)
    print(f'Release: {version}\nSource: deployed beta {beta}\nTarget: main {main}')
    print('Caller checkout and uncommitted files will be left untouched.')
    print('Schema review required.' if schema else 'Schema and migration engine are unchanged.')
    if dry_run:
        print('Dry run: no release branch, commit, PR, tag, or deployment created.')
        return
    candidates = [runtime.ref(remote, branch) for remote in ['gitea', 'origin']]
    local = runtime.git('rev-parse', '--verify', 'refs/heads/' + branch, allow_failure=True)
    candidates += [local or '']
    revisions = set(filter(None, candidates))
    if len(revisions) > 1:
        raise ReleaseError('Release branch revisions differ; refusing to reset or overwrite any copy')
    if revisions:
        sha = revisions.pop()
        manifest, existing_notes = validate_branch(runtime, sha, version, beta, main)
        if notes_file and notes.strip() != existing_notes.strip():
            raise ReleaseError('Existing release notes differ; review and commit edits in the retained release checkout')
        notes = existing_notes
    else:
        directory = runtime.common / 'helm-releases' / version / 'checkout'
        if directory.exists():
            raise ReleaseError(f'Incomplete release checkout retained at {directory}; inspect it before retrying')
        directory.parent.mkdir(parents=True, exist_ok=True)
        runtime.git('worktree', 'add', '-b', branch, str(directory), beta)
        if not runtime.ancestor(main, beta):
            try:
                runtime.git('merge', '--no-commit', '--no-ff', main, cwd=directory)
            except ReleaseError:
                raise ReleaseError(f'Main/beta merge needs review in {directory}; caller checkout is unchanged') from None
        changed = runtime.git('diff', '--name-only', beta, cwd=directory).splitlines()
        if any(not metadata_path(path) for path in changed):
            raise ReleaseError(f'Main adds application changes absent from tested beta. Inspect {directory}; '
                               'merge those changes into beta and deploy it before release preparation.')
        manifest = {'format': 1, 'version': version, 'beta_sha': beta, 'main_sha': main,
                    'beta_run_id': beta_evidence['run_id'], 'beta_image': beta_evidence['image'],
                    'notes': f'docs/releases/{version}.md'}
        for name in ['VERSION', MANIFEST, manifest['notes'], 'docs/releases/unreleased.md']:
            path = directory / name
            if path.is_symlink() or not path.resolve().is_relative_to(directory.resolve()):
                raise ReleaseError('Release metadata must stay inside its isolated checkout')
        bounded_write(directory / 'VERSION', version[1:] + '\n')
        bounded_write(directory / MANIFEST, json.dumps(manifest, indent=2) + '\n')
        bounded_write(directory / manifest['notes'], notes)
        bounded_write(directory / 'docs/releases/unreleased.md',
                      f'# Unreleased (on `beta`)\n\nChanges after [Helm {version}]({version}.md).\n\n'
                      '## Changes\n\nNo changes recorded yet.\n')
        runtime.git('add', 'VERSION', MANIFEST, manifest['notes'], 'docs/releases/unreleased.md', cwd=directory)
        runtime.git('commit', '-m', f'Prepare Helm {version} release', cwd=directory)
        sha = runtime.git('rev-parse', 'HEAD', cwd=directory)
        validate_branch(runtime, sha, version, beta, main)
        print('Release checkout:', directory)
    if runtime.pair('beta') != beta or runtime.pair('main') != main:
        raise ReleaseError('Beta/main advanced during preparation; no release branch was published')
    runtime.ready('beta', beta)
    runtime.publish_branch(branch, sha)
    prs = runtime.prs(branch)
    if len(prs) > 1 or (prs and (prs[0]['state'] != 'OPEN' or prs[0]['headRefOid'] != sha)):
        raise ReleaseError('An existing PR does not match this open release; inspect it before proceeding')
    if prs:
        url = prs[0]['url']
    else:
        schema_detail = '\n'.join('- `' + path + '`' for path in schema) or 'No schema changes.'
        body = (f'Prepare Helm {version} from verified beta `{beta}` for production.\n\n'
                f'Beta CI and deployment: {beta_evidence["url"]}\n\n'
                f'Release notes: `{manifest["notes"]}`. Only release metadata differs from tested beta.\n\n'
                '**Merging this PR deploys production through the existing main workflow.**\n\n'
                'Before merging:\n\n'
                '- [ ] Review release notes and the PR diff.\n'
                '- [ ] Required main PR checks pass for this exact head.\n'
                '- [ ] Fresh verified production backup and current rollback image recorded.\n'
                '- [ ] Populated-data upgrade and retained-image compatibility verified if schema changes.\n\n'
                f'Schema comparison against `{main}`:\n\n{schema_detail}\n\n'
                f'After merge and successful production deployment: `make finish-release VERSION={version}`.\n'
                'That command verifies the merge revision before publishing tags and release notes to both hosts.\n')
        with tempfile.TemporaryDirectory(prefix='helm-release-pr-') as directory:
            path = Path(directory) / 'body.md'
            bounded_write(path, body)
            url = runtime.gh('pr', 'create', '--base', 'main', '--head', branch,
                             '--title', f'Release Helm {version}', '--body-file', str(path), '--no-maintainer-edit')
    print('Prepared PR:', url)
    print('Next: review the backup/compatibility checklist and merge after required checks pass.')
    print(f'After production is ready: make finish-release VERSION={version}')
