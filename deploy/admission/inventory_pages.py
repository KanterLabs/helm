"""Bounded two-level authenticated inventory pages for the CLOSED journal."""
import hashlib
import os

from .filesystem import BootstrapError, MAX_ENTRIES, canonical, lower_hex, strict_json

PAGE_LIMIT = 16000
FANOUT = 64
MAX_PAGES = 2048
MAX_INDEXES = 32


def descriptor(name, body):
    return dict(name=name, sha256=hashlib.sha256(body).hexdigest())


def build(records):
    """Return record files, index files, and bounded root descriptors."""
    pages, current = [], []
    for record in records:
        if len(canonical(current + [record])) > PAGE_LIMIT:
            if not current:
                raise BootstrapError('inventory entry exceeds durable page bound')
            pages.append(canonical(current))
            current = []
        current.append(record)
    if current:
        pages.append(canonical(current))
    if not 1 <= len(pages) <= MAX_PAGES:
        raise BootstrapError('durable inventory page count exceeded')
    files = [('inventory-' + str(i).zfill(4) + '.json', body) for i, body in enumerate(pages)]
    descriptors = [descriptor(name, body) for name, body in files]
    indexes = [('inventory-index-' + str(i // FANOUT).zfill(4) + '.json',
                canonical(descriptors[i:i + FANOUT])) for i in range(0, len(descriptors), FANOUT)]
    if len(indexes) > MAX_INDEXES or any(len(body) > PAGE_LIMIT for _, body in indexes):
        raise BootstrapError('durable inventory index bound exceeded')
    return files, indexes, [descriptor(name, body) for name, body in indexes]


def checked_descriptor(value, name):
    if (not isinstance(value, dict) or set(value) != {'name', 'sha256'}
            or value['name'] != name or not lower_hex(value['sha256'], 64)):
        raise BootstrapError('invalid durable inventory descriptor order')


def checked_page(directory, value, read):
    body = read(directory, value['name'], maximum=PAGE_LIMIT)
    if hashlib.sha256(body).hexdigest() != value['sha256']:
        raise BootstrapError('durable inventory hash differs')
    page = strict_json(body)
    if not isinstance(page, list) or not page or canonical(page) != body:
        raise BootstrapError('invalid noncanonical durable inventory page')
    return page


def validate_records(records):
    if not 2 <= len(records) <= MAX_ENTRIES:
        raise BootstrapError('durable inventory entry count exceeded')
    paths = []
    numeric = {'device', 'inode', 'uid', 'gid', 'mode', 'mtime_ns', 'size'}
    for record in records:
        if not isinstance(record, dict):
            raise BootstrapError('invalid durable inventory entry')
        kind, path = record.get('kind'), record.get('path')
        fields = {'path', 'kind'} | numeric | ({'sha256'} if kind in ('file', 'symlink') else set())
        if (set(record) != fields or kind not in ('file', 'directory', 'symlink')
                or not isinstance(path, str) or len(os.fsencode(path)) > 4096):
            raise BootstrapError('invalid durable inventory entry schema')
        parts = path.split('/')
        if parts[0] not in ('data', 'config') or len(parts) > 65 or any(p in ('', '.', '..') for p in parts):
            raise BootstrapError('invalid durable inventory path')
        if any(type(record[key]) is not int or (key != 'mtime_ns' and record[key] < 0) for key in numeric):
            raise BootstrapError('invalid durable inventory numeric metadata')
        if kind in ('file', 'symlink') and not lower_hex(record['sha256'], 64):
            raise BootstrapError('invalid durable inventory content hash')
        if len(parts) == 1 and kind != 'directory':
            raise BootstrapError('durable inventory root must be a directory')
        paths.append(path)
    key = lambda path: (0 if path.split('/')[0] == 'data' else 1, tuple(path.split('/')[1:]))
    if (paths != sorted(paths, key=key) or len(set(paths)) != len(paths)
            or 'data' not in paths or 'config' not in paths):
        raise BootstrapError('invalid durable inventory canonical ordering')
    kinds = {r['path']: r['kind'] for r in records}
    for path in paths:
        parent = path.rpartition('/')[0]
        if parent and kinds.get(parent) != 'directory':
            raise BootstrapError('invalid durable inventory parent')


def load(directory, roots, read):
    if not isinstance(roots, list) or not 1 <= len(roots) <= MAX_INDEXES:
        raise BootstrapError('invalid durable inventory index count')
    records, files, page_number = [], [], 0
    for index, root in enumerate(roots):
        name = 'inventory-index-' + str(index).zfill(4) + '.json'
        checked_descriptor(root, name)
        descriptors = checked_page(directory, root, read)
        if not 1 <= len(descriptors) <= FANOUT or (index != len(roots) - 1 and len(descriptors) != FANOUT):
            raise BootstrapError('invalid durable inventory index fanout')
        files.append(name)
        for value in descriptors:
            if page_number >= MAX_PAGES:
                raise BootstrapError('durable inventory page count exceeded')
            name = 'inventory-' + str(page_number).zfill(4) + '.json'
            checked_descriptor(value, name)
            page = checked_page(directory, value, read)
            if len(records) + len(page) > MAX_ENTRIES:
                raise BootstrapError('durable inventory entry count exceeded')
            records.extend(page)
            files.append(name)
            page_number += 1
    validate_records(records)
    return records, files
