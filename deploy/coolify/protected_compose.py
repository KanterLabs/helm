#!/usr/bin/env python3
"""Render the guarded private runtime profile for a verified operator rollout."""
import json
from pathlib import Path
import subprocess
import sys


def render(lane):
    if lane not in ('beta', 'production'):
        raise ValueError('exact beta or production lane required')
    source = Path(__file__).with_name('compose.py')
    raw = subprocess.check_output([sys.executable, str(source), lane], timeout=10)
    if len(raw) > 20000:
        raise ValueError('private profile exceeds bound')
    profile = json.loads(raw)
    if set(profile) != {'services'} or set(profile['services']) != {'helm'}:
        raise ValueError('expected the existing single Helm service')
    app = profile['services']['helm']
    root = '/srv/helm/' + lane
    if (app['user'] != '65532:65532' or app['cap_drop'] != ['ALL']
            or app['security_opt'] != ['no-new-privileges:true'] or app['read_only'] is not True
            or app['volumes'] != [root + '/data:/var/lib/roadmap/data', root + '/config:/etc/roadmap:ro']):
        raise ValueError('existing profile drift requires review')
    app['user'] = '70541:70541'
    app['entrypoint'] = ['/usr/local/bin/helm-admission']
    app['tmpfs'] = [v.replace('uid=65532,gid=65532', 'uid=70541,gid=70541') for v in app['tmpfs']]
    app['volumes'].append(root + '/security:/etc/helm-admission:ro')
    return profile


if __name__ == '__main__':
    if len(sys.argv) != 2:
        raise SystemExit('usage: protected_compose.py beta|production')
    print(json.dumps(render(sys.argv[1]), indent=2))
