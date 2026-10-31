"""Single-primary activation/recovery. Never select the retained legacy pair."""
import json

from rollback_policy import FLOOR_KEY, MINIMUM, PolicyError


def coordinates(current):
    return {'image': current['HELM_IMAGE'], 'sha': current['HELM_RELEASE_SHA']}


def transition(controller):
    c = controller
    previous = c.preflight()
    floor = c.policy.floor(previous[FLOOR_KEY])
    c.policy.fresh()
    c.evidence.update(previous_image=previous['HELM_IMAGE'], previous_revision=previous['HELM_RELEASE_SHA'])
    c.evidence['authorization_plan'] = {
        'minimum': MINIMUM, 'lane': c.policy.target['lane'],
        'bundle_sha256': c.policy.checksum, 'candidate': c.policy.candidate,
        'recovery': c.policy.recovery, 'stop_observer_sha256': c.witness.checksum}
    pair = coordinates(previous)
    c.floor_snapshot = previous[FLOOR_KEY]
    # An exact healthy revision can be replayed only after floor activation.
    if pair == c.policy.candidate and floor['minimum'] == MINIMUM:
        try:
            c.evidence.update(c.wait(c.image, c.sha))
        except (c.error_type, PolicyError) as exc:
            # Replay is still a live writer. Failed policy verification must
            # not bypass the stop-only path merely because no new start ran.
            c.evidence.update(status='failed-replay-verification', deployment_error=str(exc))
            try:
                c.halt()
                c.evidence['unverified_stop_requested'] = True
            except (c.error_type, PolicyError) as halt_error:
                c.evidence['unverified_stop_error'] = str(halt_error)
            c.save()
            raise c.error_type('existing deployment verification failed; stop requested') from None
        c.evidence['authorization'] = c.policy.provenance(previous[FLOOR_KEY], c.witness.checksum)
        c.evidence['status'] = 'already-current'
        c.save()
        return
    c.evidence['status'] = 'stopping'
    c.save()
    try:
        c.stop(pair)
        # No candidate has started yet. Unknown PATCH outcome leaves everything
        # stopped, even if a subsequent GET might show the write committed.
        wanted = json.dumps(c.policy.advanced(floor), sort_keys=True, separators=(',', ':'))
        c.require_stopped(pair)
        if floor['minimum'] < MINIMUM:
            c.patch_env(FLOOR_KEY, wanted)
        else:
            wanted = previous[FLOOR_KEY]
        if c.current()[FLOOR_KEY] != wanted:
            raise PolicyError('durable authorization floor readback mismatch')
        c.floor_snapshot = wanted
        c.evidence['authorization'] = c.policy.provenance(wanted, c.witness.checksum)
    except (c.error_type, PolicyError) as exc:
        c.evidence.update(status='failed-policy-closed', deployment_error=str(exc))
        c.save()
        raise c.error_type('activation stopped at authorization barrier: ' + str(exc)) from None
    c.evidence['status'] = 'activating'
    c.save()
    try:
        c.activate(c.policy.candidate, pair, wanted)
        c.evidence.update(c.wait(c.image, c.sha))
        c.evidence['status'] = 'deployed'
    except (c.error_type, PolicyError) as original:
        c.evidence['deployment_error'] = str(original)
        try:
            # Read configured coordinates afresh: either PATCH may have committed
            # before a lost response. The observer checks actual host identities.
            failed = coordinates(c.preflight())
            c.stop(failed)
            c.activate(c.policy.recovery, failed, wanted)
            recovery = c.policy.recovery
            c.evidence['recovery'] = c.wait(recovery['image'], recovery['sha'])
            c.evidence['recovery_image'] = recovery['image']
            c.evidence['recovery_revision'] = recovery['sha']
            c.evidence['status'] = 'failed-recovery-verified'
        except (c.error_type, PolicyError) as recovery_error:
            c.evidence.update(status='failed-recovery-failed', recovery_error=str(recovery_error))
            try:
                c.stop(coordinates(c.preflight()))
            except (c.error_type, PolicyError) as stop_error:
                c.evidence['stop_error'] = str(stop_error)
                try:
                    c.halt()
                    c.evidence['unverified_stop_requested'] = True
                except (c.error_type, PolicyError) as halt_error:
                    c.evidence['unverified_stop_error'] = str(halt_error)
            raise c.error_type('deployment failed; accepted recovery failed; operator inspection required') from None
        raise c.error_type('deployment failed (' + str(original) + '); accepted recovery verified') from None
    finally:
        c.save()
