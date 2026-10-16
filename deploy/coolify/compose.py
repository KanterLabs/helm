#!/usr/bin/env python3
"""Render the secret-free, private single-primary Coolify service definition."""
import json
import sys

lane = sys.argv[1]
if lane not in ('beta', 'production'):
    raise SystemExit('lane must be beta or production')
subnet = '172.28.25' if lane == 'beta' else '172.28.26'
port = 18446 if lane == 'beta' else 18447
root = '/srv/helm/' + lane
print(json.dumps({
    'services': {'helm': {
        'image': '${HELM_IMAGE:?immutable image required}',
        'restart': 'unless-stopped',
        'init': True,
        'user': '65532:65532',
        'working_dir': '/var/lib/roadmap/data',
        'stop_grace_period': '30s',
        'read_only': True,
        'cap_drop': ['ALL'],
        'security_opt': ['no-new-privileges:true'],
        'tmpfs': ['/tmp:rw,noexec,nosuid,nodev,size=64m', '/data:rw,noexec,nosuid,nodev,size=1m,uid=65532,gid=65532'],
        'env_file': [root + '/config/runtime.env'],
        'environment': {'HELM_RELEASE_SHA': '${HELM_RELEASE_SHA:?release revision required}'},
        'ports': ['10.0.30.21:' + str(port) + ':8443'],
        'volumes': [root + '/data:/var/lib/roadmap/data', root + '/config:/etc/roadmap:ro'],
        # network_mode prevents Coolify from adding a second default network.
        # The pre-provisioned network allocates only its fixed .2 address.
        'network_mode': 'helm-' + lane,
        'healthcheck': {'test': ['CMD', '/usr/local/bin/helm', 'healthcheck'], 'interval': '10s', 'timeout': '5s', 'start_period': '20s', 'retries': 6},
        'logging': {'driver': 'json-file', 'options': {'max-size': '10m', 'max-file': '3'}},
    }},
}, indent=2))
