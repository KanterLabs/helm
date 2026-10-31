"""Clearly synthetic, non-accepting deployment provenance for command fixtures."""


def authorization(lane, sha, image):
    target = {'lane': lane, 'service_uuid': 'fixture-' + lane, 'service_name': 'fixture-' + lane,
              'project_uuid': 'fixture-project', 'environment_uuid': 'fixture-' + lane,
              'server_uuid': 'fixture-server', 'site_url': 'https://fixture.home.shanekanterman.dev',
              'api_url': 'https://coolify.home.shanekanterman.dev/api/v1',
              'data_identity': 'fixture-' + lane + '/database.sqlite'}
    return {'format': 1, 'minimum': 41, 'lane': lane, 'target': target,
            'bundle_sha256': 'd' * 64, 'candidate': {'image': image, 'sha': sha},
            'recovery': {'image': 'ghcr.io/kanterlabs/helm@sha256:' + 'c' * 64, 'sha': 'c' * 40},
            'stop_observer_sha256': 'e' * 64, 'floor_sha256': 'f' * 64}
