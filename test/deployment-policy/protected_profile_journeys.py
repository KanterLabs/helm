#!/usr/bin/env python3
"""Protected profile interface and actual historical-image refusal journeys."""
import importlib.util
import json
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
OLD = 'ghcr.io/kanterlabs/helm@sha256:5484aee2b817d5cd54987a7c24b1ed9b54176430302d6f08eacf8abb59d29cdd'


def run():
    spec = importlib.util.spec_from_file_location('protected_compose', ROOT / 'deploy/coolify/protected_compose.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    receipts = []
    for lane in ('beta', 'production'):
        old = json.loads(subprocess.check_output([sys.executable, str(ROOT / 'deploy/coolify/compose.py'), lane], text=True))
        new = module.render(lane)
        assert set(new['services']) == {'helm'} and set(new) == set(old)
        old_app, app = old['services']['helm'], new['services']['helm']
        changed = {'user', 'entrypoint', 'tmpfs', 'volumes'}
        assert {k: v for k, v in old_app.items() if k not in changed} == {
            k: v for k, v in app.items() if k not in changed}
        assert app['user'] == '70541:70541'
        assert app['entrypoint'] == ['/usr/local/bin/helm-admission']
        assert app['volumes'] == old_app['volumes'] + [f'/srv/helm/{lane}/security:/etc/helm-admission:ro']
        assert app['tmpfs'] == [v.replace('uid=65532,gid=65532', 'uid=70541,gid=70541') for v in old_app['tmpfs']]
        assert app['cap_drop'] == ['ALL'] and app['security_opt'] == ['no-new-privileges:true']
        assert app['read_only'] is True
        receipts.append(dict(case=lane + '-profile-preserves-private-service', passed=True))
    try:
        module.render('foreign')
    except ValueError:
        pass
    else:
        raise AssertionError('foreign lane accepted')
    receipts.append(dict(case='foreign-lane-refused', passed=True))
    name = 'helm-profile-refusal-' + secrets.token_hex(6)
    with tempfile.TemporaryDirectory(prefix='helm-profile-') as folder:
        args = ['docker', 'run', '--name', name, '--network', 'none', '--read-only',
                '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--user', '70541:70541',
                '--mount', f'type=bind,src={folder},dst=/var/lib/roadmap/data',
                '--entrypoint', '/usr/local/bin/helm-admission', OLD]
        try:
            result = subprocess.run(args, text=True, capture_output=True, timeout=20)
            assert result.returncode != 0 and 'helm-admission' in result.stderr
            assert any(v in result.stderr.lower() for v in ('no such file', 'not found'))
            receipts.append(dict(case='actual-historical-image-missing-reserved-entrypoint', passed=True,
                                 exit_code=result.returncode))
        finally:
            subprocess.run(['docker', 'rm', '-f', name], capture_output=True, timeout=20)
    out = ROOT / '.artifacts/tc335/protected-profile'
    out.mkdir(parents=True, exist_ok=True)
    (out / 'journeys.json').write_text(json.dumps(dict(cases=receipts), indent=2) + '\n')
    print(f'{len(receipts)} protected profile journeys passed')


if __name__ == '__main__':
    run()
