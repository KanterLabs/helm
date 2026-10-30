"""Bounded Git, GitHub, Gitea and readiness operations for release commands."""
from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.error
import urllib.request

from release_authorization import validate as validate_authorization
from rollback_policy import PolicyError, strict_json

REPOSITORY = 'KanterLabs/helm'
GITEA_API = 'https://gitea.home.shanekanterman.dev/api/v1/repos/' + REPOSITORY
SITES = {'beta': 'https://beta-helm.home.shanekanterman.dev',
         'production': 'https://helm.home.shanekanterman.dev'}
REMOTES = {'gitea': 'https://gitea.home.shanekanterman.dev/KanterLabs/helm.git',
           'origin': 'https://github.com/KanterLabs/helm.git'}
SHA = re.compile(r'[0-9a-f]{40}\Z')
IMAGE = re.compile(r'ghcr\.io/kanterlabs/helm@sha256:[0-9a-f]{64}\Z')
VERSION = re.compile(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z')
FILE_LIMIT = 20_000


class ReleaseError(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def bounded_write(path, text):
    path = Path(path)
    if len(text.encode()) > FILE_LIMIT or (path.exists() and path.stat().st_size > FILE_LIMIT):
        raise ReleaseError(f'{path.name} exceeds the 20,000-byte file limit')
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def version_tuple(value):
    match = VERSION.fullmatch(value)
    if not match:
        raise ReleaseError('Version must be a stable tag such as v0.2.2')
    return tuple(int(part) for part in match.groups())


class Runtime:
    def __init__(self):
        self.root = Path(self.run(['git', 'rev-parse', '--show-toplevel']))
        self.common = Path(self.git('rev-parse', '--path-format=absolute', '--git-common-dir'))
        for remote, expected in REMOTES.items():
            for flags in [[], ['--push']]:
                if self.git('remote', 'get-url', '--all', *flags, remote) != expected:
                    raise ReleaseError(f'{remote} must point to {expected}; no writes performed')

    def run(self, args, *, cwd=None, allow_failure=False):
        env = dict(os.environ)
        # Credentials never enter argv, Git config files, output or release evidence.
        token = env.pop('GITEA_TOKEN', '')
        if args[0] == 'git' and token:
            index = int(env.get('GIT_CONFIG_COUNT', '0'))
            env[f'GIT_CONFIG_KEY_{index}'] = 'http.https://gitea.home.shanekanterman.dev/.extraheader'
            env[f'GIT_CONFIG_VALUE_{index}'] = 'Authorization: Basic ' + base64.b64encode(
                ('shane:' + token).encode()).decode()
            env['GIT_CONFIG_COUNT'] = str(index + 1)
        try:
            result = subprocess.run(args, cwd=cwd, env=env, text=True,
                                    capture_output=True, timeout=180)
        except (OSError, subprocess.TimeoutExpired):
            raise ReleaseError(f'{args[0]} could not finish; check connectivity and authentication') from None
        if result.returncode:
            if allow_failure:
                return None
            raise ReleaseError(f'{args[0]} {args[1]} failed; no force or automatic retry was attempted')
        return result.stdout.strip()

    def git(self, *args, cwd=None, allow_failure=False):
        return self.run(['git', *args], cwd=cwd or self.root, allow_failure=allow_failure)

    def gh(self, *args, json_result=False, allow_failure=False):
        result = self.run(['gh', *args, '--repo', REPOSITORY], cwd=self.root, allow_failure=allow_failure)
        return json.loads(result) if json_result and result is not None else result

    def api(self, path):
        # gh api does not accept --repo; its path is pinned to this repository.
        return json.loads(self.run(['gh', 'api', f'repos/{REPOSITORY}/{path}'], cwd=self.root))

    def ref(self, remote, name, *, tag=False):
        ref = f'refs/{"tags" if tag else "heads"}/{name}'
        output = self.git('ls-remote', remote, ref, ref + '^{}')
        values = dict(line.split()[::-1] for line in output.splitlines())
        sha = values.get(ref + '^{}', values.get(ref, ''))
        if sha and not SHA.fullmatch(sha):
            raise ReleaseError('Remote returned an invalid revision')
        return sha

    def pair(self, name):
        canonical, mirror = self.ref('gitea', name), self.ref('origin', name)
        if not canonical or canonical != mirror:
            raise ReleaseError(f'{name} differs between Gitea and GitHub; reconcile it without force-pushing')
        return canonical

    def fetch(self):
        # Private refs avoid changing caller branches or existing remote-tracking refs.
        for remote in REMOTES:
            self.git('fetch', '--no-tags', remote,
                     f'+refs/heads/*:refs/helm-release/{remote}/*')

    def ancestor(self, older, newer):
        return self.git('merge-base', '--is-ancestor', older, newer, allow_failure=True) is not None

    def publish_branch(self, name, sha):
        for remote in REMOTES:
            existing = self.ref(remote, name)
            if existing and existing != sha:
                raise ReleaseError(f'{remote}/{name} already points elsewhere; refusing overwrite')
        for remote in REMOTES:
            if self.ref(remote, name) != sha:
                self.git('push', remote, f'{sha}:refs/heads/{name}')

    def request(self, url, *, body=None, missing_ok=False, authenticated=False):
        headers = {'Accept': 'application/json', 'User-Agent': 'helm-release/1'}
        if authenticated:
            headers['Authorization'] = 'token ' + os.environ['GITEA_TOKEN']
        data = None if body is None else json.dumps(body).encode()
        if data is not None:
            headers['Content-Type'] = 'application/json'
        try:
            request = urllib.request.Request(url, data=data, headers=headers)
            with urllib.request.build_opener(NoRedirect).open(request, timeout=20) as response:
                payload = response.read(1_048_577)
            if len(payload) > 1_048_576:
                raise ReleaseError('Release service returned an oversized response')
            return json.loads(payload)
        except urllib.error.HTTPError as error:
            if error.code == 404 and missing_ok:
                return None
            raise ReleaseError(f'Release service returned HTTP {error.code}') from None
        except (OSError, ValueError):
            raise ReleaseError('Release service is unreachable or returned invalid JSON') from None

    def gitea(self, path, **kwargs):
        return self.request(GITEA_API + path, authenticated=True, **kwargs)

    def ready(self, lane, sha):
        for endpoint in ['healthz', 'readyz']:
            value = self.request(SITES[lane] + '/' + endpoint)
            if (value.get('service') != 'helm' or value.get('status') != 'ok'
                    or value.get('revision') != sha
                    or (endpoint == 'readyz' and value.get('ready') is not True)):
                raise ReleaseError(f'{lane} is not ready at {sha[:12]}; wait for its exact deployment')

    def deployed(self, lane, sha):
        branch, job = ('beta', 'beta_deploy') if lane == 'beta' else ('main', 'deploy')
        runs = self.api(f'actions/workflows/ci.yml/runs?branch={branch}&head_sha={sha}&event=push&per_page=20')
        matches = [r for r in runs['workflow_runs'] if r.get('head_sha') == sha
                   and r.get('head_branch') == branch and r.get('event') == 'push']
        if not matches or matches[0].get('status') != 'completed' or matches[0].get('conclusion') != 'success':
            raise ReleaseError(f'{branch} CI has not passed for {sha[:12]}; rerun after CI finishes')
        run = matches[0]
        jobs = self.api(f'actions/runs/{run["id"]}/jobs?per_page=100')['jobs']
        results = {item['name']: item.get('conclusion') for item in jobs}
        if any(results.get(name) != 'success' for name in ['checks', 'browser', 'race', 'container', job]):
            raise ReleaseError(f'{branch} checks or deployment did not succeed')
        self.ready(lane, sha)
        with tempfile.TemporaryDirectory(prefix='helm-release-evidence-') as directory:
            self.gh('run', 'download', str(run['id']), '--name', f'helm-coolify-{lane}-{sha}', '--dir', directory)
            path = Path(directory) / 'deployment.json'
            if not path.is_file() or path.stat().st_size > FILE_LIMIT:
                raise ReleaseError('Deployment evidence is missing or oversized')
            try:
                evidence = strict_json(path.read_text())
            except PolicyError:
                raise ReleaseError('Deployment evidence has invalid bounded JSON') from None
        if (any(evidence.get(key) != sha for key in ['revision', 'health_revision', 'ready_revision'])
                or evidence.get('ready') is not True
                or evidence.get('status') not in ['deployed', 'already-current']
                or not IMAGE.fullmatch(evidence.get('image', ''))):
            raise ReleaseError('Deployment evidence does not match the verified release')
        try:
            authorization = validate_authorization(evidence.get('authorization'), lane, sha,
                                                  evidence['image'], evidence.get('service_uuid'))
            if evidence.get('service_uuid') != authorization['target']['service_uuid']:
                raise PolicyError('deployment service is absent or mismatched')
        except PolicyError:
            raise ReleaseError('Deployment authorization provenance is absent or mismatched') from None
        return {'run_id': run['id'], 'url': run['html_url'], 'image': evidence['image'],
                'authorization': authorization}

    def prs(self, branch):
        return self.gh('pr', 'list', '--head', branch, '--base', 'main', '--state', 'all',
                       '--json', 'number,url,state,headRefName,baseRefName,headRefOid,body,mergeCommit,mergedAt',
                       json_result=True)
