#!/usr/bin/env python3
"""Real-process skill update journey through an isolated local Git transport.

Failure inventory recorded before changes: split CLI modules or references can
be omitted; an old updater may fail to install its replacement; a hook import
can break; no-op updates can fetch again; credentials, unrelated hooks, session
state or trust configuration can be replaced; hook entries can be duplicated.
No live service, real credential, global Git config or actual Codex home is used.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

REPO = Path(__file__).resolve().parents[2]
SOURCE = REPO / 'skills/helm'
REMOTE = 'https://github.com/KanterLabs/helm.git'


def run(command, *, cwd=None, env=None, payload=None):
    result = subprocess.run(command, cwd=cwd, env=env, input=payload,
                            capture_output=True, text=True, timeout=90)
    assert result.returncode == 0, result.stderr[-1500:]
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--receipt', type=Path, required=True)
    args = parser.parse_args()
    real_git = shutil.which('git')
    assert real_git
    with tempfile.TemporaryDirectory(prefix='helm-skill-update-') as temporary:
        root = Path(temporary)
        source_repo = root / 'source'
        # Reuse existing, normally committed repository history. Creating fresh
        # fixture commits would re-stage grandfathered large files and invoke
        # unrelated global commit hooks; no hook is disabled or bypassed here.
        run([real_git, 'clone', '--quiet', '--bare', '--shared', str(REPO), str(source_repo)])
        old_revision = run([real_git, 'rev-parse', 'origin/main'], cwd=REPO).strip()
        new_revision = run([real_git, 'rev-parse', 'HEAD'], cwd=REPO).strip()
        assert old_revision != new_revision, 'commit the reviewed skill change before the publication rehearsal'
        run([real_git, '--git-dir', str(source_repo), 'update-ref', 'refs/heads/main', old_revision])
        bootstrap = root / 'bootstrap'
        bootstrap.mkdir()
        for name in ['update_skill.py', 'install_hooks.py']:
            raw = subprocess.check_output([real_git, 'show', f'origin/main:skills/helm/scripts/{name}'], cwd=REPO)
            (bootstrap / name).write_bytes(raw)
        git_config = root / 'gitconfig'
        git_config.write_text(f'[url "{source_repo.as_uri()}"]\n\tinsteadOf = {REMOTE}\n[protocol "file"]\n\tallow = always\n')
        trace = root / 'git-invocations.jsonl'
        wrapper_dir = root / 'bin'
        wrapper_dir.mkdir()
        wrapper = wrapper_dir / 'git'
        wrapper.write_text('#!/usr/bin/python3\nimport json,os,sys\n'
                           f'with open({str(trace)!r},"a") as f:f.write(json.dumps(sys.argv[1:])+"\\n")\n'
                           f'os.execv({real_git!r},[{real_git!r},*sys.argv[1:]])\n')
        wrapper.chmod(0o700)
        codex_directory = root / 'codex'
        target = codex_directory / 'skills/helm'
        target.parent.mkdir(parents=True)
        hooks = codex_directory / 'hooks.json'
        hooks.write_text(json.dumps({'hooks': {'PreToolUse': [{'matcher': 'fixture', 'hooks': [{'type': 'command', 'command': 'unrelated-fixture-policy'}]}]}}))
        hooks.chmod(0o600)
        protected = {codex_directory / 'config.toml': b'# synthetic trust fixture\n',
                     root / 'state/session.json': b'{"task_id":"synthetic-task"}\n',
                     root / 'credentials.json': b'{"base_url":"https://helm.example.test","token":"synthetic-fixture-token"}\n'}
        for path, contents in protected.items():
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(contents)
            path.chmod(0o600)
        # Configure test transport and credential location without repurposing
        # HOME or CODEX_HOME, or inheriting real agent/service credentials.
        env = {'PATH': str(wrapper_dir) + ':' + os.environ.get('PATH', '/usr/bin:/bin'),
               'GIT_CONFIG_GLOBAL': str(git_config), 'GIT_CONFIG_NOSYSTEM': '1',
               'PYTHONDONTWRITEBYTECODE': '1', 'HELM_CONFIG': str(root / 'credentials.json')}
        updater = bootstrap / 'update_skill.py'
        first = json.loads(run(['python3', str(updater), '--target', str(target)], env=env))
        assert first == {'updated': True, 'revision': old_revision}
        installed_updater = target / 'scripts/update_skill.py'
        before_hooks = hooks.read_bytes()
        before_script = hashlib.sha256((target / 'scripts/helm_hook.py').read_bytes()).hexdigest()
        run([real_git, '--git-dir', str(source_repo), 'update-ref', 'refs/heads/main', new_revision, old_revision])
        second = json.loads(run(['python3', str(installed_updater), '--target', str(target)], env=env))
        assert second == {'updated': True, 'revision': new_revision}
        assert hooks.read_bytes() == before_hooks
        assert hashlib.sha256((target / 'scripts/helm_hook.py').read_bytes()).hexdigest() != before_script
        for path, contents in protected.items():
            assert path.read_bytes() == contents and path.stat().st_mode & 0o777 == 0o600
        source_modules = sorted(SOURCE.glob('scripts/helm_*.py'))
        for module in source_modules:
            assert (target / 'scripts' / module.name).read_bytes() == module.read_bytes()
        assert (target / 'references/task-images.md').read_bytes() == (SOURCE / 'references/task-images.md').read_bytes()
        helper = target / 'scripts/helm.py'
        assert '--image' in run(['python3', str(helper), 'comment', '--help'], env=env)
        assert 'upload' in run(['python3', str(helper), 'attachments', '--help'], env=env)
        config = json.loads(run(['python3', str(helper), 'config-check'], env=env))
        assert config['origin'] == 'https://helm.example.test'
        hook_result = json.loads(run(['python3', str(target / 'scripts/helm_hook.py')],
                                     env=env, payload='{"hook_event_name":"SessionStart"}'))
        assert isinstance(hook_result, dict)
        trace.write_text('')
        third = json.loads(run(['python3', str(installed_updater), '--target', str(target)], env=env))
        assert third == {'updated': False, 'revision': new_revision}
        invocations = [json.loads(line) for line in trace.read_text().splitlines()]
        assert len(invocations) == 1 and invocations[0][0] == 'ls-remote', invocations
        assert hooks.read_bytes() == before_hooks
        result = dict(result='passed',old_updater_installs_new_package=True,
                      copied_helper_modules=len(source_modules),image_commands_import=True,
                      hook_script_updated=True,hook_configuration_preserved=True,
                      unrelated_hooks_credentials_state_and_trust_preserved=True,
                      no_op_only_checks_remote_revision=True,network='isolated local Git transport')
    args.receipt.parent.mkdir(parents=True, exist_ok=True)
    args.receipt.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result))


if __name__ == '__main__':
    main()
