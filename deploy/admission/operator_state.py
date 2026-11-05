"""Bounded append-only native transition history, outside the runtime marker."""
import hashlib
import os
import re
import stat

from rollback_policy import artifact, target_valid

from .authority import check_lock, read_authority, write_new
from .bootstrap import coordinates
from .filesystem import (BootstrapError, PRIVATE_ID, bounded_names, canonical, digest,
                         lower_hex, no_acl, strict_json)
from .inventory_pages import MAX_INDEXES, MAX_PAGES
from .operator_plan import upgraded_pair
from . import host_rebind

MAX_TRANSITIONS = 256
ORIGIN = 'operator-origin.json'
ORIGIN_KEYS = {'format', 'bootstrap_plan_sha256', 'bootstrap_metadata_sha256', 'initial_marker',
               'host_context_sha256', 'target', 'artifacts', 'authorization_floor',
               'config_device', 'config_inode'}
INTENT_KEYS = {'format', 'sequence', 'previous_intent_sha256', 'plan_sha256', 'action',
               'before', 'marker', 'host_context_sha256', 'data_snapshot_sha256', 'config_snapshot_sha256'}
UPGRADE_KEYS = INTENT_KEYS | {'artifacts', 'acceptance_sha256'}
REBIND_KEYS = INTENT_KEYS | {'previous_host_context_sha256'}
MARKER_KEYS = {'format', 'minimum_authorization', 'phase', 'lane', 'generation', 'operation_nonce',
               'data_device', 'data_inode', 'lock_device', 'lock_inode', 'binary_sha256',
               'role', 'image', 'release_sha'}


def raw_hash(body):
    return hashlib.sha256(body).hexdigest()


def marker_valid(value):
    if (not isinstance(value, dict) or set(value) != MARKER_KEYS or type(value['format']) is not int
            or value['format'] != 1 or type(value['minimum_authorization']) is not int
            or value['minimum_authorization'] != 41 or value['phase'] not in ('prepared', 'active')
            or value['role'] not in ('candidate', 'recovery')):
        raise BootstrapError('unsupported runtime marker')
    coordinates(value['lane'], value['image'], value['release_sha'], value['binary_sha256'], value['operation_nonce'])
    for key in ('generation', 'data_device', 'data_inode', 'lock_device', 'lock_inode'):
        if type(value[key]) is not int or not 0 < value[key] < 2 ** 64:
            raise BootstrapError('invalid runtime marker identity')
    if len(canonical(value)) > 4096:
        raise BootstrapError('runtime marker bound exceeded')


def current(directory, lock):
    metadata = os.fstat(directory)
    if (metadata.st_uid, metadata.st_gid, stat.S_IMODE(metadata.st_mode)) != (0, PRIVATE_ID, 0o750):
        raise BootstrapError('prepared private authority metadata required')
    no_acl(directory)
    body = read_authority(directory, 'marker.json', mode=0o640, gid=PRIVATE_ID, maximum=4096)
    marker = strict_json(body)
    marker_valid(marker)
    if canonical(marker) != body:
        raise BootstrapError('noncanonical runtime marker refused')
    check_lock(directory, lock, marker)
    return marker, raw_hash(body)


def files(directory):
    names = bounded_names(directory, MAX_PAGES + MAX_INDEXES + MAX_TRANSITIONS + 5)
    bootstrap, intents = [], []
    for name in names:
        if name in ('marker.json', 'runtime.lock', ORIGIN):
            continue
        if name == 'journal.json' or re.fullmatch(r'inventory-(?:index-)?[0-9]{4}\.json', name):
            bootstrap.append([name, raw_hash(read_authority(directory, name))])
        elif re.fullmatch(r'operator-intent-[0-9]{6}\.json', name):
            intents.append(name)
        else:
            raise BootstrapError('unknown or interrupted authority metadata; manual reconciliation required')
    expected = ['operator-intent-' + str(n).zfill(6) + '.json' for n in range(1, len(intents) + 1)]
    if intents != expected or len(intents) > MAX_TRANSITIONS:
        raise BootstrapError('transition history gap or bound exceeded')
    return digest(bootstrap), intents


def read_json(directory, name):
    body = read_authority(directory, name)
    value = strict_json(body)
    if not isinstance(value, dict) or canonical(value) != body:
        raise BootstrapError('noncanonical durable operator metadata')
    return value


def history(directory, origin, names):
    if (not isinstance(origin, dict) or set(origin) != ORIGIN_KEYS
            or type(origin['format']) is not int or origin['format'] != 1):
        raise BootstrapError('unsupported original operator authority')
    for key in ('bootstrap_plan_sha256', 'bootstrap_metadata_sha256', 'host_context_sha256'):
        if not lower_hex(origin[key], 64):
            raise BootstrapError('invalid original operator digest')
    for key in ('config_device', 'config_inode'):
        if type(origin[key]) is not int or not 0 < origin[key] < 2 ** 64:
            raise BootstrapError('invalid original config identity')
    target_valid(origin['target'])
    floor = origin['authorization_floor']
    if (not isinstance(floor, dict) or set(floor) != {'format', 'minimum', 'target', 'activation'}
            or type(floor['format']) is not int or floor['format'] != 1
            or type(floor['minimum']) is not int or floor['minimum'] != 41
            or floor['target'] != origin['target']):
        raise BootstrapError('original floor41 authority malformed')
    activation = floor['activation']
    if (not isinstance(activation, dict) or set(activation) != {'bundle_sha256', 'candidate', 'recovery'}
            or not lower_hex(activation['bundle_sha256'], 64)):
        raise BootstrapError('original floor activation malformed')
    if not isinstance(origin['artifacts'], dict) or set(origin['artifacts']) != {'candidate', 'recovery'}:
        raise BootstrapError('original accepted pair malformed')
    for role in ('candidate', 'recovery'):
        accepted = origin['artifacts'][role]
        if not isinstance(accepted, dict) or set(accepted) != {'image', 'sha', 'binary_sha256'}:
            raise BootstrapError('original image source and binary coordinates malformed')
        artifact(activation[role])
        if activation[role] != {key: accepted[key] for key in ('image', 'sha')}:
            raise BootstrapError('original activation artifact pair differs')
        coordinates(origin['target']['lane'], accepted['image'], accepted['sha'],
                    accepted['binary_sha256'], origin['initial_marker']['operation_nonce'])
    marker_valid(origin['initial_marker'])
    if origin['initial_marker']['phase'] != 'prepared':
        raise BootstrapError('operator origin must be CLOSED')
    previous, before, marker = None, origin['initial_marker'], origin['initial_marker']
    records = []
    accepted = before_accepted = origin['artifacts']
    context = origin['host_context_sha256']
    for sequence, name in enumerate(names, 1):
        record = read_json(directory, name)
        upgrade = record.get('format') == 2 and record.get('action') == 'upgrade'
        rebind = record.get('format') == 3 and record.get('action') == host_rebind.ACTION
        keys = REBIND_KEYS if rebind else (UPGRADE_KEYS if upgrade else INTENT_KEYS)
        if (not isinstance(record, dict) or set(record) != keys
                or type(record['format']) is not int or record['format'] != (3 if rebind else (2 if upgrade else 1))
                or type(record['sequence']) is not int or record['sequence'] != sequence
                or record['previous_intent_sha256'] != (digest(previous) if previous else digest(origin))
                or record['action'] not in ('close', 'activate', 'upgrade', host_rebind.ACTION)
                or (record['action'] == 'upgrade' and not upgrade)
                or (record['action'] == host_rebind.ACTION and not rebind)):
            raise BootstrapError('invalid durable transition chain')
        context = host_rebind.advance(record, context)
        source_accepted = accepted
        if record['before'] != marker:
            if record['action'] not in ('close', host_rebind.ACTION) or record['before'] != before:
                raise BootstrapError('only fresh close can cancel a pending transition')
            source_accepted = before_accepted
        marker_valid(record['before'])
        marker_valid(record['marker'])
        desired = record['marker']
        if rebind and any(desired[key] != record['before'][key]
                          for key in ('role', 'image', 'release_sha', 'binary_sha256')):
            raise BootstrapError('host rebind must preserve selected accepted artifact')
        if upgrade:
            if (record['before']['phase'] != 'prepared' or desired['role'] != 'candidate'
                    or not lower_hex(record['acceptance_sha256'], 64)):
                raise BootstrapError('upgrade requires CLOSED candidate and accepted bundle binding')
            upgraded_pair(source_accepted, record['artifacts'], desired['lane'], desired['operation_nonce'])
            accepted = record['artifacts']
        else:
            accepted = source_accepted
        fixed = MARKER_KEYS - {'phase', 'generation', 'operation_nonce', 'image', 'release_sha', 'binary_sha256', 'role'}
        if (any(desired[key] != origin['initial_marker'][key] for key in fixed)
                or desired['generation'] != record['before']['generation'] + 1
                or desired['phase'] != ('active' if record['action'] == 'activate' else 'prepared')
                or (record['action'] == 'activate' and record['before']['phase'] != 'prepared')):
            raise BootstrapError('invalid durable marker transition')
        selected = accepted[desired['role']]
        if (desired['image'], desired['release_sha'], desired['binary_sha256']) != (
                selected['image'], selected['sha'], selected['binary_sha256']):
            raise BootstrapError('durable transition artifact differs')
        for key in ('plan_sha256', 'data_snapshot_sha256', 'config_snapshot_sha256'):
            if not lower_hex(record[key], 64):
                raise BootstrapError('invalid durable transition digest')
        if desired['operation_nonce'] in [origin['initial_marker']['operation_nonce'],
                                         *(r['marker']['operation_nonce'] for r in records)]:
            raise BootstrapError('durable transition nonce reused')
        records.append(record)
        before_accepted = source_accepted
        previous, before, marker = record, record['before'], desired
    return records


def accepted_pair(origin, records, marker):
    """Resolve either endpoint of a validated latest (possibly pending) intent."""
    pairs = {digest(origin['initial_marker']): origin['artifacts']}
    for record in records:
        selected = (record['artifacts'] if record['action'] == 'upgrade'
                    else pairs[digest(record['before'])])
        pairs[digest(record['marker'])] = selected
    return pairs[digest(marker)]


def switch(directory, marker):
    # Never remove an interrupted temp. It is uncertain state requiring review.
    write_new(directory, 'operator-marker.next', canonical(marker), mode=0o640, gid=PRIVATE_ID)
    os.rename('operator-marker.next', 'marker.json', src_dir_fd=directory, dst_dir_fd=directory)
    os.fsync(directory)
