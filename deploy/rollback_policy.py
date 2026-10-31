"""Checksum-rooted runtime acceptance and monotonic authorization floor.

The protected bundle checksum is the trust anchor, supplied separately from
the selected application revision. TC336 produces the referenced observations;
this reader validates their binding and completeness, not runtime behavior.
"""
from __future__ import annotations

import hashlib
import json
import re
import time
from pathlib import Path

IMAGE_RE = re.compile(r'ghcr\.io/kanterlabs/helm@sha256:[0-9a-f]{64}\Z')
SHA_RE = re.compile(r'[0-9a-f]{40}\Z')
HASH_RE = re.compile(r'[0-9a-f]{64}\Z')
NAME_RE = re.compile(r'[a-zA-Z0-9_-][a-zA-Z0-9_.-]{0,127}\Z')
FLOOR_KEY = 'HELM_AUTHORIZATION_FLOOR'
MINIMUM = 41
CAPABILITIES = {'membership-authority-v1', 'legacy-session-invalidation-v1', 'current-data-recovery-v1'}
SCENARIOS = {'restricted-human', 'zero-grant-human', 'pending-disabled-human',
             'configured-email-demotion', 'bearer-scope-intersection', 'aggregate-visibility',
             'session-revoke-reset', 'disable-reactivation', 'schema35-populated-upgrade',
             'schema40-populated-upgrade', 'current-data-recovery-rollforward'}
TARGET_KEYS = {'lane', 'service_uuid', 'service_name', 'project_uuid', 'environment_uuid',
               'server_uuid', 'site_url', 'api_url', 'data_identity'}


class PolicyError(Exception):
    pass


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def strict_json(raw):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise PolicyError('duplicate JSON key')
            result[key] = value
        return result
    try:
        if len(raw.encode() if isinstance(raw, str) else raw) > 20_000:
            raise PolicyError('policy JSON exceeds limit')
        return json.loads(raw, object_pairs_hook=unique,
                          parse_constant=lambda _: (_ for _ in ()).throw(PolicyError('invalid JSON constant')))
    except (ValueError, UnicodeError, TypeError, RecursionError):
        raise PolicyError('invalid policy JSON') from None


def keys(value, expected, context):
    if not isinstance(value, dict) or set(value) != set(expected):
        raise PolicyError(context + ' fields mismatch')


def artifact(value):
    keys(value, {'image', 'sha'}, 'artifact')
    if (not isinstance(value['image'], str) or not IMAGE_RE.fullmatch(value['image'])
            or not isinstance(value['sha'], str) or not SHA_RE.fullmatch(value['sha'])):
        raise PolicyError('artifact must be an exact immutable digest/source-SHA pair')
    return dict(value)


def bounded_file(path):
    path = Path(path)
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 20_000:
        raise PolicyError('policy input missing, symlinked or oversized')
    raw = path.read_bytes()
    if len(raw) > 20_000:
        raise PolicyError('policy input exceeds limit')
    return raw


def target_valid(target):
    keys(target, TARGET_KEYS, 'target')
    if target['lane'] not in {'beta', 'production'}:
        raise PolicyError('unknown deployment lane')
    if any(not isinstance(v, str) or not v or len(v) > 512 or any(ord(c) < 32 for c in v)
           for v in target.values()):
        raise PolicyError('invalid target value')


class Acceptance:
    def __init__(self, path, checksum, target, candidate, recovery):
        target_valid(target)
        self.target = dict(target)
        self.candidate, self.recovery = artifact(candidate), artifact(recovery)
        if self.candidate['image'] == self.recovery['image'] or self.candidate['sha'] == self.recovery['sha']:
            raise PolicyError('recovery must use a distinct immutable image and source revision')
        if not isinstance(checksum, str) or not HASH_RE.fullmatch(checksum):
            raise PolicyError('protected acceptance checksum missing or invalid')
        self.checksum = checksum
        raw = bounded_file(path)
        if digest(raw) != checksum:
            raise PolicyError('acceptance bundle checksum mismatch')
        value = strict_json(raw)
        keys(value, {'format', 'kind', 'producer', 'issued_at', 'expires_at', 'target', 'artifacts'}, 'bundle')
        if (type(value['format']) is not int or value['format'] != 1
                or value['kind'] != 'helm-runtime-acceptance-v1'
                or value['producer'] != 'tc336-isolated-runtime-v1' or value['target'] != target):
            raise PolicyError('acceptance format, producer or target mismatch')
        self.issued, self.expires = value['issued_at'], value['expires_at']
        self.fresh()
        keys(value['artifacts'], {'candidate', 'recovery'}, 'bundle artifacts')
        for role, expected in [('candidate', candidate), ('recovery', recovery)]:
            item = value['artifacts'][role]
            keys(item, {'image', 'sha', 'capabilities', 'proofs'}, 'accepted artifact')
            if artifact({k: item[k] for k in ['image', 'sha']}) != expected:
                raise PolicyError(role + ' artifact mismatch')
            caps = item['capabilities']
            if (not isinstance(caps, list) or not all(isinstance(c, str) for c in caps)
                    or len(caps) != len(CAPABILITIES) or set(caps) != CAPABILITIES):
                raise PolicyError('unknown or missing authorization capabilities')
            keys(item['proofs'], SCENARIOS, 'runtime proofs')
            for scenario, proof in item['proofs'].items():
                keys(proof, {'file', 'sha256'}, 'proof reference')
                if (not isinstance(proof['file'], str) or not NAME_RE.fullmatch(proof['file'])
                        or not isinstance(proof['sha256'], str) or not HASH_RE.fullmatch(proof['sha256'])):
                    raise PolicyError('invalid runtime proof reference')
                captured = bounded_file(Path(path).parent / proof['file'])
                if digest(captured) != proof['sha256']:
                    raise PolicyError('runtime proof checksum mismatch')
                record = strict_json(captured)
                keys(record, {'format', 'scenario', 'target', 'subject', 'result', 'observations'}, 'runtime proof')
                if (type(record['format']) is not int or record['format'] != 1
                        or record['scenario'] != scenario or record['target'] != target
                        or record['subject'] != expected or record['result'] != 'passed'):
                    raise PolicyError('runtime proof binding or result mismatch')
                observations = record['observations']
                if not isinstance(observations, list) or not 1 <= len(observations) <= 128:
                    raise PolicyError('missing bounded runtime observations')
                for observation in observations:
                    keys(observation, {'assertion', 'passed', 'exchange'}, 'runtime observation')
                    if (not isinstance(observation['assertion'], str) or not 1 <= len(observation['assertion']) <= 512
                            or observation['passed'] is not True):
                        raise PolicyError('failed runtime observation')
                    exchange = observation['exchange']
                    keys(exchange, {'method', 'path', 'status', 'expected_status', 'response',
                                    'expected_response'}, 'runtime exchange')
                    if (exchange['method'] not in ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']
                            or not isinstance(exchange['path'], str)
                            or not exchange['path'].startswith('/') or len(exchange['path']) > 256
                            or '?' in exchange['path'] or '#' in exchange['path']
                            or type(exchange['status']) is not int or not 100 <= exchange['status'] <= 599
                            or type(exchange['expected_status']) is not int
                            or exchange['status'] != exchange['expected_status']
                            or not isinstance(exchange['response'], dict)
                            or exchange['response'] != exchange['expected_response']):
                        raise PolicyError('runtime exchange does not match accepted expectation')

    def fresh(self):
        now = int(time.time())
        if (type(self.issued) is not int or type(self.expires) is not int
                or not 0 <= now - self.issued <= 86400 or not now < self.expires <= self.issued + 86400):
            raise PolicyError('stale or invalid acceptance interval')

    def floor(self, raw):
        value = strict_json(raw)
        if not isinstance(value, dict) or set(value) not in [
                {'format', 'minimum', 'target'}, {'format', 'minimum', 'target', 'activation'}]:
            raise PolicyError('invalid authorization floor fields')
        if (type(value['format']) is not int or value['format'] != 1
                or type(value['minimum']) is not int or value['minimum'] not in [0, MINIMUM]
                or value['target'] != self.target):
            raise PolicyError('authorization floor unknown, decreased or target mismatch')
        if value['minimum'] == MINIMUM:
            keys(value.get('activation'), {'bundle_sha256', 'candidate', 'recovery'}, 'floor activation')
            if not HASH_RE.fullmatch(str(value['activation']['bundle_sha256'])):
                raise PolicyError('invalid floor acceptance checksum')
            artifact(value['activation']['candidate'])
            artifact(value['activation']['recovery'])
        elif 'activation' in value:
            raise PolicyError('unactivated floor contains activation')
        return value

    def advanced(self, current):
        if current['minimum'] == MINIMUM:
            return current
        return {'format': 1, 'minimum': MINIMUM, 'target': self.target,
                'activation': {'bundle_sha256': self.checksum,
                               'candidate': self.candidate, 'recovery': self.recovery}}

    def provenance(self, floor_raw, observer_hash):
        floor = self.floor(floor_raw)
        if floor['minimum'] != MINIMUM:
            raise PolicyError('authorization floor has not been advanced')
        return {'format': 1, 'minimum': MINIMUM, 'lane': self.target['lane'],
                'target': self.target, 'bundle_sha256': self.checksum,
                'candidate': self.candidate, 'recovery': self.recovery,
                'stop_observer_sha256': observer_hash, 'floor_sha256': digest(floor_raw.encode())}
