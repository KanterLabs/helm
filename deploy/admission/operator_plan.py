"""Strict private operator inputs; no acceptance producer or binary extractor."""
from pathlib import Path

from rollback_policy import Acceptance, NAME_RE, artifact, target_valid
from .bootstrap import coordinates
from .filesystem import BootstrapError, canonical, lower_hex, read_private_json

KEYS = {'format', 'operation', 'action', 'lane', 'role', 'target', 'artifacts',
        'authorization_floor', 'bootstrap_plan', 'acceptance_bundle', 'acceptance_sha256',
        'expected_marker_sha256', 'expected_generation', 'operation_nonce'}


def pair(item):
    return {key: item[key] for key in ('image', 'sha')}


def load(path):
    plan = read_private_json(Path(path))
    if (not isinstance(plan, dict) or set(plan) != KEYS or type(plan['format']) is not int
            or (plan['format'], plan['operation'], plan['action']) not in (
                (1, 'native-admission-transition', 'close'),
                (1, 'native-admission-transition', 'activate'),
                (2, 'native-admission-upgrade', 'upgrade'))
            or plan['role'] not in ('candidate', 'recovery')
            or (plan['action'] == 'upgrade' and plan['role'] != 'candidate')):
        raise BootstrapError('unsupported native operator plan')
    target_valid(plan['target'])
    if plan['target']['lane'] != plan['lane']:
        raise BootstrapError('operator target lane mismatch')
    if not isinstance(plan['artifacts'], dict) or set(plan['artifacts']) != {'candidate', 'recovery'}:
        raise BootstrapError('exact accepted artifact pair required')
    for item in plan['artifacts'].values():
        if not isinstance(item, dict) or set(item) != {'image', 'sha', 'binary_sha256'}:
            raise BootstrapError('exact image source and binary coordinates required')
        artifact(pair(item))
        coordinates(plan['lane'], item['image'], item['sha'], item['binary_sha256'], plan['operation_nonce'])
    candidate, recovery = (plan['artifacts'][role] for role in ('candidate', 'recovery'))
    if candidate['image'] == recovery['image'] or candidate['sha'] == recovery['sha']:
        raise BootstrapError('candidate and recovery image and source must be distinct')
    if (type(plan['expected_generation']) is not int or not 0 < plan['expected_generation'] < 2 ** 64 - 1
            or not lower_hex(plan['expected_marker_sha256'], 64)
            or not lower_hex(plan['acceptance_sha256'], 64)):
        raise BootstrapError('invalid marker CAS or acceptance coordinates')
    for key in ('bootstrap_plan', 'acceptance_bundle'):
        value = plan[key]
        if not isinstance(value, str) or not value or len(value.encode()) > 4096:
            raise BootstrapError('bounded private absolute operator paths required')
        path = Path(value)
        if not path.is_absolute() or '..' in path.parts:
            raise BootstrapError('private absolute operator paths required')
    return plan


def upgraded_pair(current, desired, lane, nonce):
    """An upgrade rotates exact current candidate into the recovery slot."""
    if not isinstance(desired, dict) or set(desired) != {'candidate', 'recovery'}:
        raise BootstrapError('exact upgrade artifact pair required')
    for item in desired.values():
        if not isinstance(item, dict) or set(item) != {'image', 'sha', 'binary_sha256'}:
            raise BootstrapError('exact upgrade binary coordinates required')
        artifact(pair(item))
        coordinates(lane, item['image'], item['sha'], item['binary_sha256'], nonce)
    if desired['recovery'] != current['candidate']:
        raise BootstrapError('upgrade recovery must exactly equal current accepted candidate')
    candidate = desired['candidate']
    if any(candidate['image'] == item['image'] or candidate['sha'] == item['sha']
           for item in current.values()):
        raise BootstrapError('upgrade candidate image and source must be new to current pair')


def accepted(plan):
    """Check root-private proof inputs before invoking the existing reader.

    Protected root maintenance is serialized externally. No untrusted writer can
    alter these private inputs between the metadata checks and Acceptance reads.
    Acceptance authenticates image/source receipts, not binary extraction.
    """
    path = Path(plan['acceptance_bundle'])
    bundle = read_private_json(path)
    if not isinstance(bundle, dict) or not isinstance(bundle.get('artifacts'), dict):
        raise BootstrapError('invalid private acceptance bundle')
    for item in bundle['artifacts'].values():
        if not isinstance(item, dict) or not isinstance(item.get('proofs'), dict):
            raise BootstrapError('invalid private acceptance proofs')
        for proof in item['proofs'].values():
            if (not isinstance(proof, dict) or not isinstance(proof.get('file'), str)
                    or not NAME_RE.fullmatch(proof['file'])):
                raise BootstrapError('invalid bounded private acceptance proof name')
            read_private_json(path.parent / proof['file'])
    acceptance = Acceptance(path, plan['acceptance_sha256'], plan['target'],
                            pair(plan['artifacts']['candidate']), pair(plan['artifacts']['recovery']))
    floor = acceptance.floor(canonical(plan['authorization_floor']))
    if plan['action'] == 'activate' and floor['minimum'] != 41:
        raise BootstrapError('activation requires independently read-back external floor41')
    return acceptance, floor
