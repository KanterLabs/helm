"""Native-root, serialized operator close/rebind and accepted activation only."""
import os
from pathlib import Path

from rollback_policy import PolicyError
from . import bootstrap, host_context, host_rebind, operator_plan as inputs, operator_state as state
from .authority import (load_state, lock_existing, pinned_authority, write_new)
from .filesystem import (BootstrapError, FLAGS, Inventory, ORIGINAL_ID, PRIVATE_ID,
                         canonical, digest, identity, open_directory, read_private_json)
from .ownership import matches_original, native_snapshots

BASE = Path('/srv/helm')


def origin_for(plan, original, marker, metadata_hash, context, floor):
    return dict(format=1, bootstrap_plan_sha256=digest(original),
                bootstrap_metadata_sha256=metadata_hash, initial_marker=marker,
                host_context_sha256=context, target=plan['target'], artifacts=plan['artifacts'],
                authorization_floor=floor, config_device=original['config_device'],
                config_inode=original['config_inode'])


def private_source(source, origin):
    initial = origin['initial_marker']
    for name in ('data', 'config'):
        expected = ((initial['data_device'], initial['data_inode']) if name == 'data'
                    else (origin['config_device'], origin['config_inode']))
        if identity(os.fstat(source.fds[name])) != expected:
            raise BootstrapError('protected source root identity changed')
    for record in source.records:
        if record['kind'] == 'symlink':
            continue  # Immutable metadata is pinned by this observation; never follow.
        if (record['uid'], record['gid']) != (PRIVATE_ID, PRIVATE_ID) or record['mode'] & 0o077:
            raise BootstrapError('private data and config metadata required')
        if (record['kind'] == 'directory' and record['mode'] != 0o700
                or record['path'] == 'data/roadmap.db' and record['mode'] != 0o600):
            raise BootstrapError('private directory and SQLite modes required')
    if 'data/roadmap.db' not in source.fds:
        raise BootstrapError('existing private SQLite main required')


def receipt(plan, origin, marker, replay):
    return dict(format=1, lane=plan['lane'], phase=marker['phase'], role=marker['role'],
                generation=marker['generation'], marker_sha256=state.raw_hash(canonical(marker)),
                plan_sha256=digest(plan), replay=replay, authorization_floor=origin['authorization_floor'],
                continuing_fence=False)


def transition(plan_path, *, base=BASE):
    """The production CLI has fixed paths and no native-context override.

    This mutates authority only. Parent installation independently verifies the
    execution profiles, alias exclusion, external floor and image/binary binding.
    """
    try:
        return _transition(plan_path, base)
    except BootstrapError:
        raise
    except PolicyError:
        raise BootstrapError('accepted runtime policy inputs refused') from None
    except (OSError, ValueError, TypeError, KeyError, UnicodeError, RecursionError):
        raise BootstrapError('operator transition uncertain or refused; preserve authority for manual review') from None


def _transition(plan_path, base):
    plan = inputs.load(plan_path)
    context = host_context.verify(base)
    original = read_private_json(Path(plan['bootstrap_plan']))
    bootstrap.validate_plan(original)
    if original['lane'] != plan['lane']:
        raise BootstrapError('original bootstrap lane differs')
    parent = open_directory(Path(base) / plan['lane'])
    directory = lock = None
    try:
        # Existing permanent lock is acquired before origin, intent or marker writes.
        directory = os.open('security', FLAGS | os.O_DIRECTORY, dir_fd=parent)
        lock = lock_existing(directory)
        pinned_authority(parent, directory)
        marker, marker_hash = state.current(directory, lock)
        metadata_hash, names = state.files(directory)
        prepared_records = None
        try:
            origin = state.read_json(directory, state.ORIGIN)
        except FileNotFoundError:
            if names:
                raise BootstrapError('lost original operator authority; no repair permitted')
            if plan['action'] == host_rebind.ACTION:
                raise BootstrapError('host rebind requires existing original operator authority')
            # CLOSED bootstrap remains a separate implementation and interface.
            journal, prepared_records = load_state(directory, lock, original, context)
            if journal['phase'] != 'prepared':
                raise BootstrapError('complete CLOSED preparation required')
            origin = None
        records = state.history(directory, origin, names) if origin is not None else []
        if origin is not None:
            if (origin['bootstrap_plan_sha256'] != digest(original)
                    or origin['bootstrap_metadata_sha256'] != metadata_hash
                    or origin['target'] != plan['target']):
                raise BootstrapError('immutable original authority or target differs')
            effective_context = host_rebind.effective_context(origin, records)
            if context != effective_context and plan['action'] != host_rebind.ACTION:
                raise BootstrapError('native host context differs; reviewed host rebind required')
            expected_markers = ([records[-1]['before'], records[-1]['marker']] if records
                                else [origin['initial_marker']])
            if marker not in expected_markers:
                raise BootstrapError('runtime marker not bound to durable operator history')
        # A completed latest replay never writes or renews any admission.
        if records and digest(plan) == records[-1]['plan_sha256'] and marker == records[-1]['marker']:
            if context != effective_context:
                raise BootstrapError('completed replay requires current effective native host context')
            return receipt(plan, origin, marker, True)
        if plan['action'] == host_rebind.ACTION:
            host_rebind.check(plan, origin, records, context, marker)
        if origin is not None:
            current_pair = state.accepted_pair(origin, records, marker)
            if plan['action'] == 'upgrade':
                inputs.upgraded_pair(current_pair, plan['artifacts'], plan['lane'], plan['operation_nonce'])
            elif current_pair != plan['artifacts']:
                raise BootstrapError('current accepted artifact pair differs')
        elif plan['action'] == 'upgrade':
            raise BootstrapError('upgrade requires existing original operator authority')
        if marker_hash != plan['expected_marker_sha256'] or marker['generation'] != plan['expected_generation']:
            raise BootstrapError('stale runtime marker hash or generation CAS')
        if records and marker == records[-1]['before'] and plan['action'] not in ('close', host_rebind.ACTION):
            raise BootstrapError('pending transition requires fresh explicit close; activation replay denied')
        if plan['action'] in ('activate', 'upgrade') and marker['phase'] != 'prepared':
            raise BootstrapError('activation or upgrade requires CLOSED prepared marker')
        if len(records) >= state.MAX_TRANSITIONS:
            raise BootstrapError('bounded transition history exhausted; no reset permitted')
        used = [original['operation_nonce'], *(record['marker']['operation_nonce'] for record in records)]
        if plan['operation_nonce'] in used:
            raise BootstrapError('operation nonce already used')
        acceptance, floor = inputs.accepted(plan)
        advanced = acceptance.advanced(floor)
        if origin is not None:
            if advanced != origin['authorization_floor'] or floor['minimum'] != 41:
                raise BootstrapError('original floor41 activation must remain unchanged')
        else:
            if (advanced['activation']['candidate'] != inputs.pair(plan['artifacts']['candidate'])
                    or advanced['activation']['recovery'] != inputs.pair(plan['artifacts']['recovery'])
                    or advanced['activation']['bundle_sha256'] != acceptance.checksum):
                raise BootstrapError('first operator authority requires exact original accepted activation')
            selected = plan['artifacts']['candidate']
            if (original['image'], original['release_sha'], original['binary_sha256']) != (
                    selected['image'], selected['sha'], selected['binary_sha256']):
                raise BootstrapError('CLOSED bootstrap candidate differs from accepted candidate')
            origin = origin_for(plan, original, marker, metadata_hash, context, advanced)
        with Inventory(parent, owners=(ORIGINAL_ID, PRIVATE_ID)) as source:
            if prepared_records is not None:
                matches_original(source.records, prepared_records, prepared=True)
            private_source(source, origin)
            if host_context.verify(base) != context:
                raise BootstrapError('native host binding changed')
            observations = native_snapshots(Path(base) / plan['lane'])
            source.rehash()
            pinned_authority(parent, directory)
            if state.current(directory, lock) != (marker, marker_hash):
                raise BootstrapError('runtime marker changed under held permanent lock')
            acceptance.fresh()
            # Origin and intent are durable before any marker switch. Never update
            # bootstrap journal, original floor/activation or permanent lock.
            if not records and state.ORIGIN not in os.listdir(directory):
                write_new(directory, state.ORIGIN, canonical(origin))
            selected = plan['artifacts'][plan['role']]
            desired = dict(marker, phase='active' if plan['action'] == 'activate' else 'prepared',
                           generation=marker['generation'] + 1, operation_nonce=plan['operation_nonce'],
                           role=plan['role'], image=selected['image'], release_sha=selected['sha'],
                           binary_sha256=selected['binary_sha256'])
            state.marker_valid(desired)
            intent = dict(format=1, sequence=len(records) + 1,
                          previous_intent_sha256=digest(records[-1] if records else origin),
                          plan_sha256=digest(plan), action=plan['action'], before=marker, marker=desired,
                          host_context_sha256=context, **observations)
            if plan['action'] == 'upgrade':
                intent.update(format=2, artifacts=plan['artifacts'],
                              acceptance_sha256=acceptance.checksum)
            elif plan['action'] == host_rebind.ACTION:
                intent.update(format=3, previous_host_context_sha256=effective_context)
            name = 'operator-intent-' + str(len(records) + 1).zfill(6) + '.json'
            write_new(directory, name, canonical(intent))
            source.rehash()
            if host_context.verify(base) != context:
                raise BootstrapError('native host binding changed before marker switch')
            acceptance.fresh()
            pinned_authority(parent, directory)
            if state.current(directory, lock) != (marker, marker_hash):
                raise BootstrapError('runtime marker CAS changed before atomic switch')
            state.switch(directory, desired)
            if state.current(directory, lock) != (desired, state.raw_hash(canonical(desired))):
                raise BootstrapError('marker switch readback uncertain')
            return receipt(plan, origin, desired, False)
    finally:
        if lock is not None:
            os.close(lock)
        if directory is not None:
            os.close(directory)
        os.close(parent)
