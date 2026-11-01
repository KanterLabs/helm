"""Read GitHub run provenance and download one immutable Actions artifact by ID."""
from datetime import datetime, timezone
import io
import zipfile

from receipt import (DIGEST, JOBS, LIMIT, REPOSITORY, SHA, WORKFLOW, ReceiptError,
                     command, sha256, strict_json)


def api(endpoint, *, binary=False):
    # gh reads its credential from its environment/keychain. Never pass it in argv.
    raw = command(['gh', 'api', endpoint])
    return raw if binary else strict_json(raw)


def run_provenance(run_id):
    prefix = 'repos/' + REPOSITORY + '/actions/'
    run = api(prefix + 'runs/' + str(run_id))
    if (run.get('id') != run_id or run.get('event') != 'push'
            or run.get('head_branch') != 'beta' or run.get('status') != 'completed'
            or run.get('conclusion') != 'success' or run.get('path') != WORKFLOW
            or run.get('repository', {}).get('full_name') != REPOSITORY
            or run.get('head_repository', {}).get('full_name') != REPOSITORY
            or not SHA.fullmatch(str(run.get('head_sha', '')))
            or type(run.get('run_attempt')) is not int or run['run_attempt'] <= 0
            or type(run.get('workflow_id')) is not int):
        raise ReceiptError('run is not a successful trusted push-to-beta workflow')
    workflow = api(prefix + 'workflows/' + str(run['workflow_id']))
    if (workflow.get('id') != run['workflow_id'] or workflow.get('path') != WORKFLOW
            or workflow.get('state') != 'active'):
        raise ReceiptError('trusted workflow identity is unavailable or changed')
    jobs = api(prefix + f'runs/{run_id}/attempts/{run["run_attempt"]}/jobs?per_page=100')
    values = jobs.get('jobs', [])
    if jobs.get('total_count') != len(values) or len(values) > 100:
        raise ReceiptError('required job inventory is incomplete')
    for name in JOBS:
        matches = [job for job in values if job.get('name') == name]
        if (len(matches) != 1 or matches[0].get('status') != 'completed'
                or matches[0].get('conclusion') != 'success'
                or matches[0].get('run_id') != run_id
                or matches[0].get('head_sha') != run['head_sha']):
            raise ReceiptError('required validation job is missing or unsuccessful')
    return run


def download_receipt(run):
    prefix = 'repos/' + REPOSITORY + '/actions/'
    listing = api(prefix + f'runs/{run["id"]}/artifacts?per_page=100')
    values = listing.get('artifacts', [])
    if listing.get('total_count') != len(values) or len(values) > 100:
        raise ReceiptError('artifact inventory is incomplete')
    name = f'helm-validation-{run["head_sha"]}-{run["run_attempt"]}'
    matches = [item for item in values if item.get('name') == name]
    if len(matches) != 1:
        raise ReceiptError('immutable validation artifact is missing or ambiguous')
    artifact = matches[0]
    if type(artifact.get('id')) is not int or artifact['id'] <= 0 or artifact.get('expired') is not False:
        raise ReceiptError('validation artifact is invalid or expired')
    try:
        expiry = datetime.fromisoformat(artifact['expires_at'].replace('Z', '+00:00'))
        if expiry <= datetime.now(timezone.utc):
            raise ReceiptError('validation artifact has expired')
    except (KeyError, ValueError, TypeError):
        raise ReceiptError('validation artifact expiry is unavailable') from None
    binding = artifact.get('workflow_run', {})
    if (binding.get('id') != run['id'] or binding.get('head_sha') != run['head_sha']
            or binding.get('head_branch') != 'beta'):
        raise ReceiptError('artifact belongs to a different producer run/source')
    digest = artifact.get('digest', '').removeprefix('sha256:')
    if artifact.get('digest') != 'sha256:' + digest or not DIGEST.fullmatch(digest):
        raise ReceiptError('immutable artifact archive digest is unavailable')
    raw = api(prefix + f'artifacts/{artifact["id"]}/zip', binary=True)
    if sha256(raw) != digest:
        raise ReceiptError('downloaded artifact digest does not match GitHub')
    try:
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            files = archive.infolist()
            if (len(files) != 1 or files[0].filename != 'validation.json'
                    or files[0].file_size > LIMIT or files[0].flag_bits & 1
                    or (files[0].external_attr >> 16) & 0o170000 == 0o120000):
                raise ReceiptError('validation artifact must contain only a bounded receipt')
            document = archive.read(files[0])
    except (zipfile.BadZipFile, RuntimeError, OSError):
        raise ReceiptError('validation artifact archive is invalid') from None
    return strict_json(document), sha256(document), artifact['id']
