"""Allowlisted policy provenance in CI artifacts and prepared releases."""
from rollback_policy import HASH_RE, MINIMUM, PolicyError, artifact, keys, target_valid


def validate(value, lane, sha, image, service=None):
    keys(value, {'format', 'minimum', 'lane', 'target', 'bundle_sha256', 'candidate',
                 'recovery', 'stop_observer_sha256', 'floor_sha256'}, 'release authorization')
    target_valid(value['target'])
    if (type(value['format']) is not int or value['format'] != 1
            or type(value['minimum']) is not int or value['minimum'] != MINIMUM
            or value['lane'] != lane or value['target']['lane'] != lane
            or artifact(value['candidate']) != {'image': image, 'sha': sha}
            or (service is not None and value['target']['service_uuid'] != service)):
        raise PolicyError('release authorization provenance mismatch')
    artifact(value['recovery'])
    if (value['candidate']['image'] == value['recovery']['image']
            or value['candidate']['sha'] == value['recovery']['sha']):
        raise PolicyError('release recovery must use a distinct image and source revision')
    for key in ['bundle_sha256', 'stop_observer_sha256', 'floor_sha256']:
        if not isinstance(value[key], str) or not HASH_RE.fullmatch(value[key]):
            raise PolicyError('release policy checksum missing or malformed')
    return value
