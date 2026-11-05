"""Streaming authenticated image transport; no binary JSON or public URLs."""
from __future__ import annotations
import hashlib
import json
import os
import tempfile
from pathlib import Path
from urllib import error, request

from helm_config import HelmError, MAX_RESPONSE_BYTES
from helm_http import _NoRedirect, _error_details, _redact_secrets

MAX_IMAGE_BYTES = 10 * 1024 * 1024


def _attachment_headers(client):
    config = client.config
    headers = {'Authorization': 'Bearer ' + config.token, 'Accept': 'application/json', 'User-Agent': 'helm-skill/1'}
    if config.cf_access_client_id:
        headers['CF-Access-Client-Id'] = config.cf_access_client_id
        headers['CF-Access-Client-Secret'] = config.cf_access_client_secret
    return headers


def _attachment_http_error(client, exc):
    try:
        message, code = _error_details(exc.read(MAX_RESPONSE_BYTES + 1))
    finally:
        exc.close()
    config = client.config
    message = _redact_secrets(message, (config.token, config.cf_access_client_id, config.cf_access_client_secret))
    return HelmError(f'Helm returned HTTP {exc.code}: {message}', status_code=exc.code, error_code=code)


def _multipart_parts(image, boundary):
    fields = image['metadata']
    prefix = bytearray()
    for name, value in fields.items():
        if value is None or value == '':
            continue
        prefix.extend(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
    prefix.extend((f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{image["filename"]}"\r\n'
                   f'Content-Type: {image["content_type"]}\r\n\r\n').encode())
    return bytes(prefix), f'\r\n--{boundary}--\r\n'.encode()


def _upload_image(client, path, image, key):
    boundary = 'helm-' + key.replace('-', '')
    prefix, suffix = _multipart_parts(image, boundary)
    # The descriptor is held throughout staging, so changing the path cannot
    # substitute a different file. Rehash each pass to detect in-place edits.
    def chunks():
        stream = image['stream']
        stream.seek(0)
        digest = hashlib.sha256()
        count = 0
        yield prefix
        while chunk := stream.read(64 * 1024):
            count += len(chunk)
            if count > image['size_bytes']:
                raise HelmError('image changed during upload; capture it again')
            digest.update(chunk)
            yield chunk
        if count != image['size_bytes'] or digest.hexdigest() != image['sha256']:
            raise HelmError('image changed during upload; capture it again')
        yield suffix
    headers = _attachment_headers(client)
    headers.update({'Content-Type': 'multipart/form-data; boundary=' + boundary,
                    'Content-Length': str(len(prefix) + image['size_bytes'] + len(suffix)),
                    'Idempotency-Key': key})
    req = request.Request(client.config.base_url + '/api/v1' + path, data=chunks(), headers=headers, method='POST')
    try:
        with request.build_opener(_NoRedirect).open(req, timeout=30) as response:
            raw = response.read(MAX_RESPONSE_BYTES + 1)
    except error.HTTPError as exc:
        raise _attachment_http_error(client, exc) from None
    except (error.URLError, TimeoutError, OSError) as exc:
        raise HelmError('image upload response was lost; reconciling staged metadata', error_code='upload_response_lost') from exc
    if len(raw) > MAX_RESPONSE_BYTES:
        raise HelmError('Helm upload response exceeded the safe size limit')
    try:
        return json.loads(raw)
    except (ValueError, UnicodeError):
        raise HelmError('Helm returned a non-JSON attachment response') from None


def _download_image(client, path, output, metadata, thumbnail=False):
    destination = Path(output).expanduser()
    if destination.exists():
        raise HelmError('download destination already exists')
    headers = _attachment_headers(client)
    headers['Accept'] = 'image/png' if thumbnail else metadata.get('content_type', 'application/octet-stream')
    req = request.Request(client.config.base_url + '/api/v1' + path, headers=headers, method='GET')
    temporary = None
    try:
        with request.build_opener(_NoRedirect).open(req, timeout=30) as response:
            with tempfile.NamedTemporaryFile(dir=destination.parent, prefix='.helm-image-', delete=False) as target:
                temporary = Path(target.name)
                count = 0
                digest = hashlib.sha256()
                signature = b''
                while chunk := response.read(64 * 1024):
                    count += len(chunk)
                    if count > MAX_IMAGE_BYTES:
                        raise HelmError('download exceeded the safe image size limit')
                    signature = (signature + chunk)[:8]
                    digest.update(chunk)
                    target.write(chunk)
            if thumbnail:
                if signature != b'\x89PNG\r\n\x1a\n':
                    raise HelmError('Helm returned an invalid PNG thumbnail')
            elif digest.hexdigest() != metadata.get('sha256') or count != metadata.get('size_bytes'):
                raise HelmError('download did not match attachment metadata')
        # link is atomic and refuses to overwrite an existing destination.
        os.link(temporary, destination)
        return {'id': metadata['id'], 'status': metadata.get('status'), 'size_bytes': count, 'sha256': digest.hexdigest()}
    except error.HTTPError as exc:
        raise _attachment_http_error(client, exc) from None
    except (error.URLError, TimeoutError, OSError) as exc:
        raise HelmError('cannot download image: ' + type(exc).__name__) from None
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
