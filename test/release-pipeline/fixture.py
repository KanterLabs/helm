"""Local HTTP service contracts; real Git refs hold all release history."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import sys
import threading
import urllib.parse

from policy_fixture import authorization


class World:
    def __init__(self, root, source):
        self.root, self.source = root, source
        self.checkout = root / 'checkout'
        self.env = {**os.environ, 'GIT_AUTHOR_NAME': 'Release Fixture',
                    'GIT_AUTHOR_EMAIL': 'release@example.test', 'GIT_COMMITTER_NAME': 'Release Fixture',
                    'GIT_COMMITTER_EMAIL': 'release@example.test', 'GITEA_TOKEN': 'synthetic-secret'}
        self.real_git = subprocess.check_output(['which', 'git'], text=True).strip()
        for name in ['canonical', 'mirror']:
            subprocess.run([self.real_git, 'init', '--bare', str(root / name)], check=True, capture_output=True)
        self.checkout.mkdir()
        self.git('init', '-b', 'main')
        (self.checkout / 'app.txt').write_text('stable application\n')
        notes = self.checkout / 'docs/releases/unreleased.md'
        notes.parent.mkdir(parents=True)
        notes.write_text('# Unreleased\n\n## Features\n\n- A synthetic feature.\n')
        (self.checkout / 'internal/db/migrations').mkdir(parents=True)
        (self.checkout / 'internal/db/migrations/001_initial.sql').write_text('-- fixture\n')
        self.git('add', '.')
        self.git('commit', '-m', 'Initial fixture')
        self.git('tag', 'v0.2.1')
        self.git('remote', 'add', 'gitea', str(root / 'canonical'))
        self.git('remote', 'add', 'origin', str(root / 'mirror'))
        for remote in ['gitea', 'origin']:
            self.git('push', remote, 'main', 'v0.2.1')
        self.git('switch', '-c', 'beta')
        (self.checkout / 'app.txt').write_text('tested beta application\n')
        self.git('add', '.')
        self.git('commit', '-m', 'Add synthetic feature')
        for remote in ['gitea', 'origin']:
            self.git('push', remote, 'beta')
        self.beta_ready = self.ref('mirror', 'beta')
        self.production_ready = self.ref('mirror', 'main')
        self.ci_status = 'completed'
        self.prs, self.github_releases, self.gitea_releases, self.calls = [], [], [], []
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), self.handler())
        self.url = f'http://127.0.0.1:{self.server.server_port}'
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.bin = root / 'bin'
        self.bin.mkdir()
        git_wrapper = self.bin / 'git'
        git_wrapper.write_text(f'''#!{sys.executable}
import os,sys
args=sys.argv[1:]
if args[:2]==['remote','get-url']:
 print('https://gitea.home.shanekanterman.dev/KanterLabs/helm.git' if args[-1]=='gitea' else 'https://github.com/KanterLabs/helm.git')
else: os.execv({self.real_git!r},[{self.real_git!r}]+args)
''')
        git_wrapper.chmod(0o755)
        gh_wrapper = self.bin / 'gh'
        gh_wrapper.write_text(f'''#!{sys.executable}
import json,sys,urllib.request
from pathlib import Path
args=sys.argv[1:]
files={{}}
for flag in ['--body-file','--notes-file']:
 if flag in args: files[flag]=Path(args[args.index(flag)+1]).read_text()
request=urllib.request.Request({(self.url + '/gh')!r},data=json.dumps({{'args':args,'files':files}}).encode(),headers={{'Content-Type':'application/json'}})
with urllib.request.urlopen(request) as response: value=json.load(response)
if 'artifact' in value:
 path=Path(args[args.index('--dir')+1]);path.mkdir(parents=True,exist_ok=True)
 (path/'deployment.json').write_text(json.dumps(value['artifact']))
print(value.get('output',''))
sys.exit(value.get('code',0))
''')
        gh_wrapper.chmod(0o755)
        self.launcher = root / 'launch.py'
        self.launcher.write_text(f'''import sys,runpy
sys.path.insert(0,{str(source / 'deploy')!r})
import release_support as support
support.GITEA_API={self.url + '/gitea'!r}
support.SITES={{'beta':{self.url + '/beta'!r},'production':{self.url + '/production'!r}}}
runpy.run_path({str(source / 'deploy/release.py')!r},run_name='__main__')
''')

    def git(self, *args, cwd=None):
        result = subprocess.run([self.real_git, *args], cwd=cwd or self.checkout,
                                env=self.env, capture_output=True, text=True, check=True)
        return result.stdout.strip()

    def ref(self, remote, name, tag=False):
        namespace = 'tags' if tag else 'heads'
        result = subprocess.run([self.real_git, '--git-dir', str(self.root / remote),
                                 'rev-parse', '--verify', f'refs/{namespace}/{name}^{{commit}}'],
                                capture_output=True, text=True)
        return result.stdout.strip() if result.returncode == 0 else ''

    def branches(self, remote):
        output = self.git('--git-dir', str(self.root / remote), 'for-each-ref',
                          '--format=%(refname:short) %(objectname)', 'refs/heads')
        return dict(line.split() for line in output.splitlines())

    def command(self, *args):
        return subprocess.run([sys.executable, str(self.launcher), *args], cwd=self.checkout,
                              env={**self.env, 'PATH': str(self.bin) + ':' + self.env['PATH']},
                              capture_output=True, text=True, timeout=30)

    def merge_release(self, version):
        target = self.root / 'merge'
        self.git('clone', str(self.root / 'mirror'), str(target))
        self.git('switch', 'main', cwd=target)
        self.git('merge', '--no-ff', '--no-edit', 'origin/release/' + version, cwd=target)
        sha = self.git('rev-parse', 'HEAD', cwd=target)
        self.git('push', 'origin', 'main', cwd=target)
        self.prs[0].update(state='MERGED', mergeCommit={'oid': sha}, mergedAt='2026-10-25T00:00:00Z')
        return sha

    def handler(self):
        world = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def respond(self, value, status=200):
                body = json.dumps(value).encode()
                self.send_response(status)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                path = urllib.parse.urlsplit(self.path).path
                if path.startswith('/beta/') or path.startswith('/production/'):
                    sha = world.beta_ready if path.startswith('/beta/') else world.production_ready
                    self.respond({'service': 'helm', 'status': 'ok', 'ready': True, 'revision': sha})
                elif path.startswith('/gitea/releases/tags/'):
                    tag = path.rsplit('/', 1)[-1]
                    found = next((r for r in world.gitea_releases if r['tag_name'] == tag), None)
                    self.respond(found or {}, 200 if found else 404)
                else:
                    self.respond({'unexpected': path}, 404)

            def do_POST(self):
                data = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                if self.path == '/gh':
                    self.respond(world.gh(data['args'], data['files']))
                elif self.path == '/gitea/releases':
                    world.gitea_releases.append(data)
                    world.calls.append('gitea release create')
                    self.respond(data)
                else:
                    self.respond({}, 404)

        return Handler

    def gh(self, args, files):
        # Record operation names only, never authorization headers or fixture paths.
        self.calls.append(' '.join(args[:2]))
        if args[:1] == ['api']:
            path = args[1]
            if '/actions/workflows/' in path:
                query = urllib.parse.parse_qs(urllib.parse.urlsplit(path).query)
                branch = query['branch'][0]
                sha = query['head_sha'][0]
                result = {'workflow_runs': [{'id': 1 if branch == 'beta' else 2,
                    'head_sha': sha, 'head_branch': branch, 'event': 'push',
                    'status': self.ci_status, 'conclusion': 'success',
                    'html_url': 'https://github.com/KanterLabs/helm/actions/runs/1'}]}
            elif path.endswith('/jobs?per_page=100'):
                deploy = 'beta_deploy' if '/1/jobs' in path else 'deploy'
                result = {'jobs': [{'name': name, 'conclusion': 'success'}
                                  for name in ['checks', 'browser', 'race', 'container', deploy]]}
            else:
                raise AssertionError(path)
            return {'output': json.dumps(result)}
        if args[:2] == ['run', 'download']:
            lane = 'beta' if args[2] == '1' else 'production'
            sha = self.ref('mirror', 'beta' if lane == 'beta' else 'main')
            image = 'ghcr.io/kanterlabs/helm@sha256:' + 'a' * 64
            return {'artifact': {'revision': sha, 'health_revision': sha, 'ready_revision': sha,
                'ready': True, 'status': 'deployed', 'image': image, 'service_uuid': 'fixture-' + lane,
                'authorization': authorization(lane, sha, image)}}
        if args[:2] == ['pr', 'list']:
            head = args[args.index('--head') + 1]
            return {'output': json.dumps([p for p in self.prs if p['headRefName'] == head])}
        if args[:2] == ['pr', 'create']:
            head = args[args.index('--head') + 1]
            value = {'number': len(self.prs) + 1, 'url': 'https://github.com/KanterLabs/helm/pull/1',
                'state': 'OPEN', 'headRefName': head, 'baseRefName': 'main', 'body': files['--body-file'],
                'headRefOid': self.ref('mirror', head), 'mergeCommit': None, 'mergedAt': None}
            self.prs.append(value)
            return {'output': value['url']}
        if args[:2] == ['release', 'view']:
            found = next((r for r in self.github_releases if r['tagName'] == args[2]), None)
            return {'output': json.dumps(found) if found else '', 'code': 0 if found else 1}
        if args[:2] == ['release', 'create']:
            self.github_releases.append({'tagName': args[2], 'body': files['--notes-file'],
                                        'isDraft': False, 'isPrerelease': False})
            return {'output': 'https://github.com/KanterLabs/helm/releases/tag/' + args[2]}
        raise AssertionError(args)

    def close(self):
        self.server.shutdown()
        self.server.server_close()
