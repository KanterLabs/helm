"""Stage task images and publish complete batches through ordinary updates."""
from __future__ import annotations
import hashlib
import os
import re
import stat
from contextlib import ExitStack
from pathlib import Path
from urllib import parse

from helm_config import HelmError
from helm_attachment_transport import MAX_IMAGE_BYTES, _upload_image, _download_image


class AttachmentError(HelmError):
    """Expose only recoverable staged IDs, never files or binary payloads."""
    def __init__(self, message, staged, operation_id):
        super().__init__(message)
        self.staged = staged
        self.operation_id = operation_id

    def as_dict(self):
        return {**super().as_dict(), 'staged_attachment_ids': self.staged, 'operation_id': self.operation_id}


def _attachment_path(task, attachment=None):
    path = '/tasks/' + parse.quote(str(task), safe='') + '/attachments'
    return path + '/' + parse.quote(str(attachment), safe='') if attachment else path


def _attachment_capabilities(client):
    try:
        payload, _ = client.call('GET', '/attachments/capabilities')
    except HelmError as exc:
        if exc.status_code == 404:
            raise HelmError('task images are unsupported by this Helm server; update the server') from None
        raise
    if not isinstance(payload, dict) or not payload.get('enabled'):
        raise HelmError('task images are unsupported or disabled by this Helm server')
    return payload


def _image_type(header):
    if header.startswith(b'\x89PNG\r\n\x1a\n'):
        return 'image/png'
    if header.startswith(b'\xff\xd8\xff'):
        return 'image/jpeg'
    if header.startswith(b'RIFF') and header[8:12] == b'WEBP':
        return 'image/webp'
    raise HelmError('image must be PNG, JPEG, or static WebP; capture it again')


def _prepare_images(args, stack, capabilities):
    paths = getattr(args, 'images', None) or []
    captions = getattr(args, 'captions', None) or []
    maximum = min(4, capabilities.get('max_images_per_update', 4))
    if not paths or len(paths) > maximum:
        raise HelmError(f'provide between 1 and {maximum} images per update')
    if captions and len(captions) != len(paths):
        raise HelmError('provide one --caption per --image, in the same order')
    metadata = {}
    for name in ('commit_sha', 'build', 'viewport_width', 'viewport_height'):
        value = getattr(args, name, None)
        if value is not None:
            if name.startswith('viewport_') and (not isinstance(value, int) or value < 1 or value > 100000):
                raise HelmError('viewport dimensions must be between 1 and 100000')
            if isinstance(value, str) and (len(value.encode('utf-8')) > (128 if name == 'commit_sha' else 200) or any(ord(c) < 32 for c in value)):
                raise HelmError('capture metadata must be short text without control characters')
            metadata[name] = value
    images = []
    for index, raw in enumerate(paths):
        path = Path(raw).expanduser()
        # Sanitize only the basename; directory names never enter metadata.
        filename = re.sub(r'[^A-Za-z0-9._ -]', '_', path.name).strip()[:180].strip()
        if not filename or filename in {'.', '..'}:
            raise HelmError('image filename is invalid')
        try:
            descriptor = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
            stream = stack.enter_context(os.fdopen(descriptor, 'rb'))
            if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
                raise HelmError('image must be a regular file')
            digest = hashlib.sha256()
            size = 0
            header = stream.read(32)
            content_type = _image_type(header)
            stream.seek(0)
            while chunk := stream.read(64 * 1024):
                size += len(chunk)
                if size > min(MAX_IMAGE_BYTES, capabilities.get('max_file_bytes', MAX_IMAGE_BYTES)):
                    raise HelmError('image exceeds the 10 MiB or server file size limit')
                digest.update(chunk)
            stream.seek(0)
        except OSError as exc:
            raise HelmError('cannot read image file: ' + type(exc).__name__) from None
        if size == 0:
            raise HelmError('image must not be empty')
        image_metadata = dict(metadata)
        if captions:
            caption = captions[index]
            if len(caption.encode('utf-8')) > 2000 or any(ord(c) < 32 and c not in '\n\t' for c in caption):
                raise HelmError('image caption must be at most 2000 characters')
            image_metadata['caption'] = caption
        images.append({'stream': stream, 'filename': filename, 'content_type': content_type,
                       'size_bytes': size, 'sha256': digest.hexdigest(), 'metadata': image_metadata})
    return images


def _matches_image(item, image):
    if not isinstance(item, dict) or not item.get('id') or item.get('status') not in {'staged', 'published'}:
        return False
    for field in ('filename', 'content_type', 'sha256', 'size_bytes'):
        if item.get(field) != image[field]:
            return False
    return all(item.get(name) == value for name, value in image['metadata'].items())


def _staged_images(client, task):
    items, _ = _paged_collection_with_cursor(client, _attachment_path(task), [('staged', 'true'), ('limit', '100')],
                                            context='attachments', collect_all=True)
    return items


def _stage_images(client, args, task):
    capabilities = _attachment_capabilities(client)
    staged = []
    with ExitStack() as stack:
        images = _prepare_images(args, stack, capabilities)
        try:
            for index, image in enumerate(images):
                path = _attachment_path(task)
                identity = {key: image[key] for key in ('filename', 'content_type', 'size_bytes', 'sha256')}
                identity.update(image['metadata'])
                identity['batch_index'] = index
                key = _command_mutation_id(args.operation_id, 'POST', path, identity)
                before = {item['id'] for item in _staged_images(client, task)}
                for attempt in range(2):
                    try:
                        item = _upload_image(client, path, image, key)
                        break
                    except HelmError as exc:
                        if exc.error_code != 'upload_response_lost':
                            raise
                        # Inspect own staged metadata before any replay. Never
                        # guess an existing unrelated upload from matching bytes.
                        candidates = [item for item in _staged_images(client, task)
                                      if item.get('id') not in before and _matches_image(item, image)]
                        if len(candidates) == 1:
                            item = candidates[0]
                            break
                        if attempt:
                            raise
                if not _matches_image(item, image):
                    raise HelmError('attachment replay did not match uploaded content and metadata')
                staged.append(item)
        except HelmError as exc:
            raise AttachmentError(str(exc), [item['id'] for item in staged], args.operation_id) from None
    return staged



def _update_images(client, args, task):
    ids = getattr(args, 'attachment_ids', None) or []
    paths = getattr(args, 'images', None) or []
    if len(ids) + len(paths) > 4 or len(set(ids)) != len(ids):
        raise HelmError('provide at most four distinct images or staged attachment IDs per update')
    if not paths and any(getattr(args, name, None) for name in
                         ('captions', 'commit_sha', 'build', 'viewport_width', 'viewport_height')):
        raise HelmError('capture metadata and captions require --image')
    existing = []
    if ids:
        capabilities = _attachment_capabilities(client)
        if len(ids) + len(paths) > capabilities.get('max_images_per_update', 4):
            raise HelmError('image batch exceeds the server limit')
        for ident in ids:
            if not isinstance(ident, str) or not ident.strip() or len(ident) > 128:
                raise HelmError('attachment IDs must be non-empty safe identifiers')
            item, _ = client.call('GET', _attachment_path(task, ident))
            if not isinstance(item, dict) or item.get('id') != ident or item.get('status') not in {'staged', 'published'}:
                raise HelmError('staged attachment is unavailable')
            existing.append(item)
    return existing + (_stage_images(client, args, task) if paths else [])


def cmd_attachments_upload(client, args):
    args.operation_id = _command_operation_id(args.operation_id)
    task = _task(client, args.task)
    images = _stage_images(client, args, task['id'])
    return {'task': task.get('key', task['id']), 'attachments': images, 'operation_id': args.operation_id}


def cmd_attachments_list(client, args):
    _attachment_capabilities(client)
    return {'data': _staged_images(client, args.task)}


def cmd_attachments_status(client, args):
    _attachment_capabilities(client)
    payload, _ = client.call('GET', _attachment_path(args.task, args.attachment))
    return payload


def cmd_attachments_delete(client, args):
    current = cmd_attachments_status(client, args)
    path = _attachment_path(args.task, args.attachment)
    operation = _command_operation_id(args.operation_id)
    payload, _ = client.call('DELETE', path, if_match=current['version'],
                            idempotency_key=_command_mutation_id(operation, 'DELETE', path, current['version']))
    return payload if payload is not None else {'id': current['id'], 'status': 'deleted'}


def cmd_attachments_download(client, args):
    current = cmd_attachments_status(client, args)
    path = _attachment_path(args.task, args.attachment) + ('/thumbnail' if args.thumbnail else '/content')
    return _download_image(client, path, args.output, current, args.thumbnail)


def cmd_comment(client, args):
    args.operation_id = _command_operation_id(args.operation_id)
    message = args.message.strip()
    if not message or len(message) > 20000:
        raise HelmError('comment must contain between 1 and 20000 characters')
    current = _task(client, args.task)
    images = _update_images(client, args, current['id'])
    body = {'body': message}
    if images:
        body['attachment_ids'] = [item['id'] for item in images]
    path = '/tasks/' + parse.quote(str(current['id']), safe='') + '/comments'
    try:
        comment, _ = client.call('POST', path, body=body,
                                idempotency_key=_command_mutation_id(args.operation_id, 'POST', path, body))
    except HelmError as exc:
        if images:
            raise AttachmentError(str(exc), body['attachment_ids'], args.operation_id) from None
        raise
    return {'task': current.get('key', current['id']), 'comment': comment, 'operation_id': args.operation_id}
