#!/usr/bin/env python3
"""Validate a retained Helm backup without touching a live installation.

This command is deliberately separate from ``helm-restore.sh``.  It opens the
selected backup read-only, copies it into a newly-created mode-0700 directory,
checks the source through its held immutable descriptor, and performs the
post-copy SQLite checks against that copy.  The
source backup, live database, service manager, and credentials are outside
the operation's write boundary.  Candidate binary migration and startup
compatibility are intentionally not executed by this slice; they are tracked
as the follow-up TC-164 sandbox work.

The stdout contract is one JSON object.  Error details from SQLite, a
candidate executable, and HTTP responses are intentionally not included in
that object: a drill report is safe to attach to an incident or CI result.
"""

from __future__ import annotations

import argparse
import base64
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import stat
import sys
import tempfile
from typing import Any, Iterable, Mapping, Sequence
from urllib.parse import quote


REPORT_SCHEMA = "helm.restore-drill.v1"
MAX_DEFAULT_BYTES = 512 * 1024 * 1024
MAX_METADATA_BYTES = 64 * 1024
MAX_CHECKSUM_BYTES = 4 * 1024
MAX_TABLES = 1_000
MAX_TABLE_NAME_BYTES = 256
MAX_MIGRATION_ROWS = 100_000

BACKUP_NAME_RE = re.compile(
    r"^roadmap-(?P<timestamp>[0-9]{8}T[0-9]{6}Z)-"
    r"(?P<release>manual|daily|pre-restore|[0-9a-f]{40})\.db$"
)
HEX64_RE = re.compile(r"^[0-9a-f]{64}$")
RELEASE_RE = re.compile(r"^(?:manual|daily|pre-restore|[0-9a-f]{40})$")
CREATED_AT_RE = re.compile(
    r"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:"
    r"[0-9]{2}:[0-9]{2}Z$"
)
INTEGER_RE = re.compile(r"^(?:0|[1-9][0-9]*)$")
LIMITATIONS = [
    "TC-163 validates one explicitly selected backup in a fresh isolated copy.",
    "The drill does not encrypt backups, fetch from an off-host repository, or schedule remote retention; those remain TC-119 scope.",
    "The drill never replaces live data, invokes service management, revokes credentials, or runs the destructive live restore helper.",
    "The selected database is a standalone SQLite snapshot; pending WAL/SHM companions are not part of the backup artifact and are ignored.",
]


class DrillError(Exception):
    """An expected, reportable failure with no sensitive detail."""

    def __init__(self, code: str, stage: str):
        super().__init__(code)
        self.code = code
        self.stage = stage


def fail(code: str, stage: str) -> None:
    raise DrillError(code, stage)


def reject_controls(value: str, code: str = "invalid_path") -> None:
    if not value or len(value) > 4096 or any(char in value for char in ("\x00", "\r", "\n")):
        fail(code, "arguments")


def ensure_absolute_path(value: str, code: str = "invalid_path") -> Path:
    reject_controls(value, code)
    path = Path(value)
    if not path.is_absolute() or any(part in {".", ".."} for part in path.parts[1:]):
        fail(code, "arguments")
    return path


def check_existing_components(path: Path, *, allow_final_missing: bool = False) -> None:
    """Reject symlink path components before opening a user-selected path."""

    parts = path.parts
    if not parts or parts[0] != os.sep:
        fail("invalid_path", "arguments")
    current = Path(os.sep)
    for index, part in enumerate(parts[1:], start=1):
        current /= part
        try:
            info = os.lstat(current)
        except FileNotFoundError:
            if allow_final_missing and index == len(parts) - 1:
                return
            fail("path_missing", "arguments")
        except OSError:
            fail("path_unavailable", "arguments")
        if stat.S_ISLNK(info.st_mode):
            fail("symlink_path", "arguments")
        if index < len(parts) - 1 and not stat.S_ISDIR(info.st_mode):
            fail("path_component_not_directory", "arguments")


def secure_regular_file(path: Path, *, expected_mode: int | None = None, code: str) -> os.stat_result:
    try:
        info = os.lstat(path)
    except FileNotFoundError:
        fail("backup_missing" if code == "backup_artifact" else "path_missing", "backup_validation")
    except OSError:
        fail("path_unavailable", "backup_validation")
    if stat.S_ISLNK(info.st_mode):
        fail("symlink_artifact", "backup_validation")
    if not stat.S_ISREG(info.st_mode):
        fail("artifact_not_regular", "backup_validation")
    if expected_mode is not None and stat.S_IMODE(info.st_mode) != expected_mode:
        fail("artifact_mode_invalid", "backup_validation")
    if info.st_size <= 0:
        fail("artifact_empty", "backup_validation")
    return info


def open_readonly_nofollow(path: Path) -> int:
    flags = os.O_RDONLY | os.O_CLOEXEC
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    try:
        return os.open(path, flags)
    except FileNotFoundError:
        fail("backup_missing", "backup_validation")
    except OSError:
        fail("backup_unreadable", "backup_validation")


def read_bounded(path: Path, maximum: int, *, expected_mode: int = 0o600) -> bytes:
    secure_regular_file(path, expected_mode=expected_mode, code="backup_artifact")
    fd = open_readonly_nofollow(path)
    try:
        info = os.fstat(fd)
        if stat.S_IMODE(info.st_mode) != expected_mode:
            fail("artifact_mode_invalid", "backup_validation")
        if info.st_size > maximum:
            fail("artifact_too_large", "backup_validation")
        chunks: list[bytes] = []
        remaining = maximum + 1
        while remaining > 0:
            block = os.read(fd, min(64 * 1024, remaining))
            if not block:
                break
            chunks.append(block)
            remaining -= len(block)
        data = b"".join(chunks)
        if len(data) > maximum:
            fail("artifact_too_large", "backup_validation")
        return data
    except DrillError:
        raise
    except OSError:
        fail("artifact_unreadable", "backup_validation")
    finally:
        os.close(fd)


def sha256_fd(fd: int, maximum: int) -> tuple[str, int]:
    digest = hashlib.sha256()
    total = 0
    offset = 0
    while True:
        try:
            block = os.pread(fd, min(1024 * 1024, maximum - total + 1), offset)
        except OSError:
            fail("backup_unreadable", "backup_validation")
        if not block:
            break
        total += len(block)
        if total > maximum:
            fail("backup_too_large", "backup_validation")
        digest.update(block)
        offset += len(block)
    return digest.hexdigest(), total


def stat_identity(info: os.stat_result) -> tuple[int, int, int, int, int, int, int]:
    return (
        info.st_dev,
        info.st_ino,
        info.st_uid,
        info.st_gid,
        info.st_size,
        info.st_mtime_ns,
        stat.S_IMODE(info.st_mode),
    )


class SourceGuard:
    """Hold the selected backup open and prove its pathname did not drift."""

    def __init__(self, path: Path, maximum: int):
        self.path = path
        self.maximum = maximum
        self.fd = -1
        secure_regular_file(path, expected_mode=0o600, code="backup_artifact")
        self.fd = open_readonly_nofollow(path)
        try:
            initial_info = os.fstat(self.fd)
            if stat.S_IMODE(initial_info.st_mode) != 0o600:
                fail("artifact_mode_invalid", "backup_validation")
            self.initial_identity = stat_identity(initial_info)
            self.initial_digest, self.initial_size = sha256_fd(self.fd, maximum)
            if self.initial_size != initial_info.st_size:
                fail("backup_changed", "backup_validation")
        except DrillError:
            self.close()
            raise
        except OSError:
            self.close()
            fail("backup_unreadable", "backup_validation")

    def copy_to(self, destination: Path) -> None:
        """Copy from the held descriptor, never by reopening the source path."""

        try:
            destination_fd = os.open(
                destination,
                os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_CLOEXEC,
                0o600,
            )
        except OSError:
            fail("isolated_copy_failed", "isolation")
        try:
            offset = 0
            while offset < self.initial_size:
                block = os.pread(self.fd, min(1024 * 1024, self.initial_size - offset), offset)
                if not block:
                    fail("backup_changed", "backup_validation")
                written = os.write(destination_fd, block)
                if written != len(block):
                    fail("isolated_copy_failed", "isolation")
                offset += written
            os.fsync(destination_fd)
            os.fchmod(destination_fd, 0o600)
        except DrillError:
            raise
        except OSError:
            fail("isolated_copy_failed", "isolation")
        finally:
            os.close(destination_fd)

        digest, copied_size = sha256_fd(self.fd, self.maximum)
        if digest != self.initial_digest or copied_size != self.initial_size:
            fail("backup_changed", "backup_validation")
        try:
            destination_info = os.lstat(destination)
        except OSError:
            fail("isolated_copy_failed", "isolation")
        if stat.S_ISLNK(destination_info.st_mode) or not stat.S_ISREG(destination_info.st_mode):
            fail("isolated_copy_failed", "isolation")
        if stat.S_IMODE(destination_info.st_mode) != 0o600:
            fail("isolated_copy_mode_invalid", "isolation")
        if destination_info.st_size != self.initial_size:
            fail("isolated_copy_failed", "isolation")

    def unchanged(self) -> bool:
        try:
            fd_info = os.fstat(self.fd)
            path_info = os.lstat(self.path)
            if stat.S_ISLNK(path_info.st_mode) or not stat.S_ISREG(path_info.st_mode):
                return False
            if stat_identity(fd_info) != self.initial_identity:
                return False
            if stat_identity(path_info) != self.initial_identity:
                return False
            digest, size = sha256_fd(self.fd, self.maximum)
            return digest == self.initial_digest and size == self.initial_size
        except OSError:
            return False

    def close(self) -> None:
        if self.fd >= 0:
            try:
                os.close(self.fd)
            except OSError:
                pass
            self.fd = -1


def parse_backup_selection(raw: str) -> tuple[Path, re.Match[str]]:
    path = ensure_absolute_path(raw)
    check_existing_components(path)
    if not stat.S_ISDIR(os.lstat(path.parent).st_mode):
        fail("backup_parent_invalid", "backup_validation")
    match = BACKUP_NAME_RE.fullmatch(path.name)
    if match is None:
        fail("backup_name_invalid", "backup_validation")
    return path, match


def parse_checksum(path: Path, backup_name: str) -> str:
    data = read_bounded(path, MAX_CHECKSUM_BYTES)
    try:
        text = data.decode("ascii")
    except UnicodeDecodeError:
        fail("checksum_malformed", "backup_validation")
    lines = text.splitlines()
    if len(lines) != 1:
        fail("checksum_malformed", "backup_validation")
    match = re.fullmatch(r"([0-9a-f]{64})[ \t]+([^ \t\r\n]+)", lines[0])
    if match is None or match.group(2) != backup_name:
        fail("checksum_malformed", "backup_validation")
    return match.group(1)


def parse_metadata(path: Path, expected_release: str) -> dict[str, str]:
    data = read_bounded(path, MAX_METADATA_BYTES)
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        fail("metadata_malformed", "backup_validation")
    if not text or not text.endswith("\n"):
        fail("metadata_malformed", "backup_validation")
    metadata: dict[str, str] = {}
    allowed = {"release_sha", "created_at", "schema_version", "migration_digest"}
    for line in text.splitlines():
        if not line or line.count("=") != 1:
            fail("metadata_malformed", "backup_validation")
        key, value = line.split("=", 1)
        if key not in allowed or key in metadata or not value or any(
            char in value for char in ("\x00", "\r", "\n")
        ):
            fail("metadata_malformed", "backup_validation")
        metadata[key] = value
    if set(metadata) - allowed or "release_sha" not in metadata or "created_at" not in metadata:
        fail("metadata_malformed", "backup_validation")
    if metadata["release_sha"] != expected_release or not RELEASE_RE.fullmatch(metadata["release_sha"]):
        fail("metadata_release_mismatch", "backup_validation")
    if not CREATED_AT_RE.fullmatch(metadata["created_at"]):
        fail("metadata_malformed", "backup_validation")
    try:
        datetime.strptime(metadata["created_at"], "%Y-%m-%dT%H:%M:%SZ")
    except ValueError:
        fail("metadata_malformed", "backup_validation")
    has_schema = "schema_version" in metadata
    has_digest = "migration_digest" in metadata
    if has_schema != has_digest:
        fail("metadata_malformed", "backup_validation")
    if has_schema and not INTEGER_RE.fullmatch(metadata["schema_version"]):
        fail("metadata_malformed", "backup_validation")
    if has_digest and not HEX64_RE.fullmatch(metadata["migration_digest"]):
        fail("metadata_malformed", "backup_validation")
    return metadata


def quote_identifier(identifier: str) -> str:
    if not identifier or len(identifier.encode("utf-8")) > MAX_TABLE_NAME_BYTES:
        fail("database_schema_invalid", "database_validation")
    return '"' + identifier.replace('"', '""') + '"'


def value_bytes(value: Any) -> bytes:
    if value is None:
        return b"N"
    if isinstance(value, bool):
        return b"I1:" + (b"1" if value else b"0")
    if isinstance(value, int):
        return b"I:" + str(value).encode("ascii")
    if isinstance(value, float):
        return b"F:" + repr(value).encode("ascii")
    if isinstance(value, bytes):
        return b"B:" + base64.b16encode(value)
    if isinstance(value, str):
        return b"T:" + value.encode("utf-8", "surrogatepass")
    return b"O:" + repr(value).encode("utf-8", "backslashreplace")


def hash_rows(rows: Iterable[Sequence[Any]]) -> str:
    digest = hashlib.sha256()
    for row in rows:
        for value in row:
            encoded = value_bytes(value)
            digest.update(len(encoded).to_bytes(8, "big"))
            digest.update(encoded)
        digest.update(b"\n")
    return digest.hexdigest()


def row_count(connection: sqlite3.Connection, table: str) -> int:
    try:
        value = connection.execute(f"SELECT COUNT(*) FROM {quote_identifier(table)}").fetchone()[0]
    except sqlite3.Error:
        fail("database_query_failed", "database_validation")
    if not isinstance(value, int) or value < 0:
        fail("database_schema_invalid", "database_validation")
    return value


def table_names(connection: sqlite3.Connection) -> list[str]:
    try:
        rows = connection.execute(
            "SELECT name FROM sqlite_master "
            "WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
        ).fetchall()
    except sqlite3.Error:
        fail("database_query_failed", "database_validation")
    if len(rows) > MAX_TABLES:
        fail("database_too_many_tables", "database_validation")
    names: list[str] = []
    for (name,) in rows:
        if not isinstance(name, str) or not name or len(name.encode("utf-8")) > MAX_TABLE_NAME_BYTES:
            fail("database_schema_invalid", "database_validation")
        names.append(name)
    return names


def table_columns(connection: sqlite3.Connection, table: str) -> tuple[list[str], list[str]]:
    try:
        rows = connection.execute(f"PRAGMA table_info({quote_identifier(table)})").fetchall()
    except sqlite3.Error:
        fail("database_schema_invalid", "database_validation")
    if not rows:
        fail("database_schema_invalid", "database_validation")
    columns = [row[1] for row in rows]
    if any(not isinstance(column, str) or not column for column in columns):
        fail("database_schema_invalid", "database_validation")
    primary = sorted(((int(row[5]), row[1]) for row in rows if int(row[5]) > 0), key=lambda item: item[0])
    primary_columns = [column for _, column in primary]
    # Every application table has a primary key.  For a legacy/custom table
    # without one, hashing the complete row still catches any data drift.
    identity_columns = primary_columns or columns
    return columns, identity_columns


def identity_digest(connection: sqlite3.Connection, table: str, identity_columns: Sequence[str]) -> str:
    quoted_columns = ", ".join(quote_identifier(column) for column in identity_columns)
    order = ", ".join(quote_identifier(column) for column in identity_columns)
    try:
        rows = connection.execute(
            f"SELECT {quoted_columns} FROM {quote_identifier(table)} ORDER BY {order}"
        )
        return hash_rows(rows)
    except sqlite3.Error:
        fail("database_query_failed", "database_validation")


def full_row_digest(
    connection: sqlite3.Connection,
    table: str,
    columns: Sequence[str],
    order_columns: Sequence[str],
) -> str:
    quoted_columns = ", ".join(quote_identifier(column) for column in columns)
    order = ", ".join(quote_identifier(column) for column in order_columns)
    try:
        rows = connection.execute(
            f"SELECT {quoted_columns} FROM {quote_identifier(table)} ORDER BY {order}"
        )
        return hash_rows(rows)
    except sqlite3.Error:
        fail("database_query_failed", "database_validation")


def schema_version(connection: sqlite3.Connection) -> int:
    names = table_names(connection)
    if "schema_migrations" not in names:
        return 0
    try:
        value = connection.execute(
            "SELECT COALESCE(MAX(version), 0) FROM \"schema_migrations\""
        ).fetchone()[0]
    except sqlite3.Error:
        fail("database_schema_invalid", "database_validation")
    if not isinstance(value, int) or value < 0:
        fail("database_schema_invalid", "database_validation")
    return value


def migration_versions(connection: sqlite3.Connection) -> tuple[int, ...]:
    names = table_names(connection)
    if "schema_migrations" not in names:
        return ()
    try:
        rows = connection.execute("SELECT version FROM \"schema_migrations\" ORDER BY version")
    except sqlite3.Error:
        fail("database_schema_invalid", "database_validation")
    versions: list[int] = []
    for index, (version,) in enumerate(rows):
        if index >= MAX_MIGRATION_ROWS:
            fail("database_too_many_migrations", "database_validation")
        if not isinstance(version, int) or version <= 0:
            fail("database_schema_invalid", "database_validation")
        versions.append(version)
    return tuple(versions)


def foreign_key_signatures(connection: sqlite3.Connection, table: str) -> dict[tuple[str, tuple[tuple[str, str], ...]], str]:
    try:
        rows = connection.execute(f"PRAGMA foreign_key_list({quote_identifier(table)})").fetchall()
    except sqlite3.Error:
        fail("database_schema_invalid", "database_validation")
    grouped: dict[int, list[tuple[Any, ...]]] = {}
    for row in rows:
        if len(row) < 5:
            fail("database_schema_invalid", "database_validation")
        grouped.setdefault(int(row[0]), []).append(row)
    signatures: dict[tuple[str, tuple[tuple[str, str], ...]], str] = {}
    for group in grouped.values():
        group.sort(key=lambda row: int(row[1]))
        parent = group[0][2]
        pairs = tuple((str(row[3]), str(row[4])) for row in group)
        if not isinstance(parent, str) or not parent or any(not child or not parent_column for child, parent_column in pairs):
            fail("database_schema_invalid", "database_validation")
        descriptor = (parent, pairs)
        columns = ", ".join(quote_identifier(child) for child, _ in pairs)
        order = columns
        try:
            relation_rows = connection.execute(
                f"SELECT {columns} FROM {quote_identifier(table)} ORDER BY {order}"
            )
            signatures[descriptor] = hash_rows(relation_rows)
        except sqlite3.Error:
            fail("database_query_failed", "database_validation")
    return signatures


def sqlite_check(connection: sqlite3.Connection) -> tuple[str, str]:
    try:
        integrity_cursor = connection.execute("PRAGMA integrity_check")
        integrity_first = integrity_cursor.fetchone()
        integrity_second = integrity_cursor.fetchone()
    except sqlite3.Error:
        fail("integrity_check_failed", "database_validation")
    if integrity_first != ("ok",) or integrity_second is not None:
        fail("integrity_check_failed", "database_validation")
    try:
        foreign_first = connection.execute("PRAGMA foreign_key_check").fetchone()
    except sqlite3.Error:
        fail("foreign_key_check_failed", "database_validation")
    if foreign_first is not None:
        fail("foreign_key_check_failed", "database_validation")
    return "ok", "ok"


class DatabaseSnapshot:
    def __init__(
        self,
        *,
        tables: Mapping[str, int],
        identities: Mapping[str, str],
        identity_columns: Mapping[str, tuple[str, ...]],
        relationships: Mapping[str, Mapping[tuple[str, tuple[tuple[str, str], ...]], str]],
        row_digests: Mapping[str, str],
        versions: tuple[int, ...],
        version: int,
    ):
        self.tables = dict(tables)
        self.identities = dict(identities)
        self.identity_columns = dict(identity_columns)
        self.relationships = {name: dict(value) for name, value in relationships.items()}
        self.row_digests = dict(row_digests)
        self.versions = versions
        self.version = version


def inspect_database(path: Path, *, source_fd: int | None = None) -> tuple[DatabaseSnapshot, dict[str, Any]]:
    try:
        # The selected source is inspected through the descriptor held by
        # SourceGuard when procfs is available.  This prevents a pathname
        # replacement between checksum validation and SQLite inspection from
        # changing which database is treated as the source snapshot.
        sqlite_path = path
        if source_fd is not None and os.path.exists("/proc/self/fd"):
            sqlite_path = Path(f"/proc/self/fd/{source_fd}")
        query = "?mode=ro"
        if source_fd is not None:
            # A retained backup is a standalone snapshot.  immutable=1 keeps
            # SQLite from consulting or creating WAL/SHM companions while
            # reading the held source descriptor.
            query += "&immutable=1"
        uri = "file:" + quote(str(sqlite_path), safe="/") + query
        connection = sqlite3.connect(uri, uri=True, timeout=5.0)
    except (sqlite3.Error, ValueError):
        fail("database_open_failed", "database_validation")
    try:
        try:
            connection.execute("PRAGMA query_only = ON")
            connection.execute("PRAGMA foreign_keys = ON")
            connection.execute("PRAGMA busy_timeout = 5000")
        except sqlite3.Error:
            fail("database_open_failed", "database_validation")
        integrity, foreign_keys = sqlite_check(connection)
        names = table_names(connection)
        tables: dict[str, int] = {}
        identities: dict[str, str] = {}
        identity_columns: dict[str, tuple[str, ...]] = {}
        relationships: dict[str, dict[tuple[str, tuple[tuple[str, str], ...]], str]] = {}
        row_digests: dict[str, str] = {}
        for name in names:
            columns, identity = table_columns(connection, name)
            tables[name] = row_count(connection, name)
            identities[name] = identity_digest(connection, name, identity)
            identity_columns[name] = tuple(identity)
            relationships[name] = foreign_key_signatures(connection, name)
            if name == "schema_migrations":
                row_digests[name] = full_row_digest(connection, name, columns, identity)
        versions = migration_versions(connection)
        version = schema_version(connection)
        snapshot = DatabaseSnapshot(
            tables=tables,
            identities=identities,
            identity_columns=identity_columns,
            relationships=relationships,
            row_digests=row_digests,
            versions=versions,
            version=version,
        )
        summary = {
            "integrity": integrity,
            "foreign_keys": foreign_keys,
            "row_counts": tables,
            "schema_version": version,
        }
        return snapshot, summary
    except DrillError:
        raise
    except sqlite3.Error:
        fail("database_query_failed", "database_validation")
    finally:
        connection.close()


def compare_snapshots(source: DatabaseSnapshot, isolated: DatabaseSnapshot) -> tuple[int, int]:
    """Check that the isolated copy has no row, identity, or FK drift."""

    if set(source.tables) != set(isolated.tables):
        fail("table_set_changed", "stable_identity_validation")
    for table in source.tables:
        if isolated.tables[table] != source.tables[table]:
            fail("table_row_count_changed", "stable_identity_validation")
        source_columns = source.identity_columns[table]
        isolated_columns = isolated.identity_columns.get(table, ())
        if source_columns != isolated_columns:
            fail("stable_identity_columns_changed", "stable_identity_validation")
        if source.identities[table] != isolated.identities[table]:
            fail("stable_identity_changed", "stable_identity_validation")

        source_relationships = source.relationships.get(table, {})
        isolated_relationships = isolated.relationships.get(table, {})
        if source_relationships.keys() != isolated_relationships.keys():
            fail("stable_relationship_missing", "relationship_validation")
        if source_relationships != isolated_relationships:
            fail("stable_relationship_changed", "relationship_validation")

    # A migration's primary key can remain stable while its applied_at value
    # changes.  Preserve and compare the complete migration rows, not only
    # their version identities.
    if source.versions != isolated.versions or source.version != isolated.version:
        fail("schema_migrations_changed", "migration_validation")
    if source.row_digests.get("schema_migrations") != isolated.row_digests.get("schema_migrations"):
        fail("schema_migrations_changed", "migration_validation")

    relationship_checks = sum(len(value) for value in source.relationships.values())
    identity_checks = len(source.tables)
    return identity_checks, relationship_checks


def validate_source_metadata(metadata: Mapping[str, str], source: DatabaseSnapshot) -> None:
    if "schema_version" not in metadata:
        return
    try:
        declared = int(metadata["schema_version"])
    except ValueError:
        fail("metadata_malformed", "backup_validation")
    if declared != source.version:
        fail("metadata_schema_mismatch", "backup_validation")


def validate_isolated_directory(path: Path) -> None:
    try:
        info = os.lstat(path)
    except OSError:
        fail("isolated_directory_failed", "isolation")
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        fail("isolated_directory_failed", "isolation")
    if stat.S_IMODE(info.st_mode) != 0o700:
        fail("isolated_directory_mode_invalid", "isolation")


def validate_isolated_database(path: Path) -> None:
    try:
        info = os.lstat(path)
    except OSError:
        fail("isolated_copy_failed", "isolation")
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        fail("isolated_copy_failed", "isolation")
    if stat.S_IMODE(info.st_mode) != 0o600 or info.st_size <= 0:
        fail("isolated_copy_mode_invalid", "isolation")


def create_isolated_directory(args: argparse.Namespace) -> tuple[Path, bool, bool]:
    if args.isolated_dir:
        path = ensure_absolute_path(args.isolated_dir)
        check_existing_components(path, allow_final_missing=True)
        if path.exists() or path.is_symlink():
            fail("isolated_directory_exists", "isolation")
        try:
            os.mkdir(path, 0o700)
        except OSError:
            fail("isolated_directory_failed", "isolation")
        validate_isolated_directory(path)
        return path, True, True

    workspace = ensure_absolute_path(args.workspace_dir)
    check_existing_components(workspace)
    try:
        workspace_info = os.lstat(workspace)
    except OSError:
        fail("workspace_unavailable", "isolation")
    if stat.S_ISLNK(workspace_info.st_mode) or not stat.S_ISDIR(workspace_info.st_mode):
        fail("workspace_unavailable", "isolation")
    try:
        path = Path(tempfile.mkdtemp(prefix=".helm-restore-drill.", dir=str(workspace)))
    except OSError:
        fail("isolated_directory_failed", "isolation")
    validate_isolated_directory(path)
    return path, False, True


def safe_remove_isolated(path: Path | None) -> bool:
    if path is None:
        return True
    try:
        info = os.lstat(path)
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
            return False
        if stat.S_IMODE(info.st_mode) != 0o700:
            return False
        # This is a fresh private directory; recurse only through regular
        # entries and directories created for this drill, and reject symlinks.
        for child in path.iterdir():
            try:
                child_info = os.lstat(child)
            except OSError:
                continue
            if stat.S_ISDIR(child_info.st_mode) and not stat.S_ISLNK(child_info.st_mode):
                if not safe_remove_isolated(child):
                    return False
            elif not stat.S_ISLNK(child_info.st_mode):
                try:
                    child.unlink()
                except OSError:
                    return False
            else:
                return False
        path.rmdir()
        return True
    except OSError:
        return False


def parser() -> argparse.ArgumentParser:
    command = argparse.ArgumentParser(
        description="Validate one retained Helm SQLite backup in an isolated disposable copy."
    )
    command.add_argument("backup_positional", nargs="?", help=argparse.SUPPRESS)
    command.add_argument("--backup", dest="backup", help="absolute path to one retained .db backup")
    command.add_argument(
        "--isolated-dir",
        "--output-dir",
        dest="isolated_dir",
        help="fresh, nonexistent absolute directory to retain the isolated copy",
    )
    command.add_argument(
        "--workspace-dir",
        default=os.environ.get("TMPDIR", "/tmp"),
        help="existing absolute parent for an automatic mode-0700 isolated directory",
    )
    # Kept as an explicit, structured refusal for callers that already know
    # the follow-up interface.  No candidate path is opened or executed here.
    command.add_argument("--candidate-binary", "--candidate", dest="candidate_binary", help=argparse.SUPPRESS)
    command.add_argument("--start-candidate", "--start", dest="start_candidate", action="store_true", help=argparse.SUPPRESS)
    command.add_argument("--max-bytes", type=int, default=MAX_DEFAULT_BYTES)
    command.add_argument("--keep-isolated", action="store_true", help="retain an automatic isolated copy on success")
    command.add_argument("--report", "--report-file", dest="report_file", help="optional path for the same JSON report")
    return command


def initial_report(selected_name: str | None = None) -> dict[str, Any]:
    return {
        "schema": REPORT_SCHEMA,
        "status": "failed",
        "operation": "isolated_restore_drill",
        "backup": {"selected": selected_name, "checksum": "not_checked", "metadata": "not_checked"},
        "source": {"unchanged": None},
        "isolation": {
            "directory_created": False,
            "copy_mode": None,
            "live_database_touched": False,
            "services_managed": False,
            "credentials_revoked": False,
            "retained": False,
        },
        "checks": {},
        "external_compatibility": {
            "status": "not_run",
            "migration": "deferred_tc_164",
            "startup": "deferred_tc_164",
        },
        "limitations": LIMITATIONS,
    }


def write_report(path: Path, report: Mapping[str, Any]) -> None:
    ensure_absolute_path(str(path), "invalid_report_path")
    check_existing_components(path, allow_final_missing=True)
    if path.exists() or path.is_symlink():
        fail("report_path_exists", "report")
    parent = path.parent
    try:
        parent_info = os.lstat(parent)
    except OSError:
        fail("report_write_failed", "report")
    if stat.S_ISLNK(parent_info.st_mode) or not stat.S_ISDIR(parent_info.st_mode):
        fail("report_write_failed", "report")
    data = (json.dumps(report, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
    temporary: str | None = None
    try:
        fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=str(parent))
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        # A hard-link publication is atomic and no-clobber: unlike rename,
        # it cannot replace a report, backup, sidecar, or live database that
        # appeared after the preflight check.
        try:
            os.link(temporary, path)
        except FileExistsError:
            os.unlink(temporary)
            temporary = None
            fail("report_path_exists", "report")
        os.unlink(temporary)
        temporary = None
    except DrillError:
        if temporary is not None:
            try:
                os.unlink(temporary)
            except OSError:
                pass
        raise
    except OSError:
        if temporary is not None:
            try:
                os.unlink(temporary)
            except OSError:
                pass
        fail("report_write_failed", "report")


def validate_report_destination(path: Path) -> None:
    ensure_absolute_path(str(path), "invalid_report_path")
    check_existing_components(path, allow_final_missing=True)
    if path.exists() or path.is_symlink():
        fail("report_path_exists", "report")
    try:
        parent_info = os.lstat(path.parent)
    except OSError:
        fail("report_write_failed", "report")
    if stat.S_ISLNK(parent_info.st_mode) or not stat.S_ISDIR(parent_info.st_mode):
        fail("report_write_failed", "report")


def run(args: argparse.Namespace, report: dict[str, Any]) -> None:
    backup_arg = args.backup
    if backup_arg and args.backup_positional:
        fail("backup_selection_ambiguous", "arguments")
    if not backup_arg:
        backup_arg = args.backup_positional
    if not backup_arg:
        fail("backup_required", "arguments")
    backup_path, name_match = parse_backup_selection(backup_arg)
    report["backup"]["selected"] = backup_path.name
    if args.max_bytes <= 0 or args.max_bytes > MAX_DEFAULT_BYTES:
        fail("max_bytes_invalid", "arguments")
    if args.report_file:
        report_path = ensure_absolute_path(args.report_file, "invalid_report_path")
        validate_report_destination(report_path)
    else:
        report_path = None

    source_guard = SourceGuard(backup_path, args.max_bytes)
    report["source"]["unchanged"] = True
    staging: Path | None = None
    automatic_staging = False
    try:
        if args.candidate_binary or args.start_candidate:
            fail("external_compatibility_deferred", "arguments")
        checksum_path = backup_path.with_name(backup_path.name + ".sha256")
        metadata_path = backup_path.with_name(backup_path.name + ".metadata")
        expected_checksum = parse_checksum(checksum_path, backup_path.name)
        metadata = parse_metadata(metadata_path, name_match.group("release"))
        report["backup"]["metadata"] = "valid"
        report["backup"]["release"] = metadata["release_sha"]
        if "schema_version" in metadata:
            report["backup"]["metadata_schema_version"] = int(metadata["schema_version"])

        actual_checksum, actual_size = sha256_fd(source_guard.fd, args.max_bytes)
        if actual_checksum != expected_checksum:
            report["backup"]["checksum"] = "mismatch"
            fail("checksum_mismatch", "backup_validation")
        report["backup"]["checksum"] = "valid"
        report["backup"]["bytes"] = actual_size

        staging, explicit_staging, automatic_staging = create_isolated_directory(args)
        report["isolation"]["directory_created"] = True
        destination = staging / "roadmap.db"
        source_guard.copy_to(destination)
        report["isolation"]["copy_mode"] = "0600"

        validate_isolated_database(destination)
        source_snapshot, source_checks = inspect_database(backup_path, source_fd=source_guard.fd)
        validate_source_metadata(metadata, source_snapshot)
        report["checks"]["source"] = source_checks

        validate_isolated_database(destination)
        isolated_snapshot, isolated_checks = inspect_database(destination)
        report["checks"]["isolated"] = isolated_checks
        identity_checks, relationship_checks = compare_snapshots(source_snapshot, isolated_snapshot)
        report["checks"]["stable_identities"] = {
            "status": "ok",
            "tables_checked": identity_checks,
            "schema_migrations": "unchanged",
        }
        report["checks"]["relationships"] = {
            "status": "ok",
            "foreign_key_relationships_checked": relationship_checks,
        }
        report["source"]["unchanged"] = source_guard.unchanged()
        if not report["source"]["unchanged"]:
            fail("source_changed", "source_invariance")
        report["status"] = "ok"
        report["isolation"]["retained"] = bool(explicit_staging or args.keep_isolated)
        if automatic_staging and not report["isolation"]["retained"]:
            if not safe_remove_isolated(staging):
                report["isolation"]["retained"] = True
                report["status"] = "failed"
                fail("isolated_cleanup_failed", "cleanup")
            staging = None
    finally:
        final_unchanged = source_guard.unchanged()
        report["source"]["unchanged"] = final_unchanged
        # A source drift discovered by the final guard must never leave an
        # otherwise successful report claiming the drill passed.  Mark it
        # before cleanup so an automatic staging directory is still removed.
        if not final_unchanged:
            report["status"] = "failed"
            report["failure"] = {"code": "source_changed", "stage": "source_invariance"}
        source_guard.close()
        if report["status"] != "ok" and staging is not None:
            if safe_remove_isolated(staging):
                staging = None
            else:
                report["isolation"]["retained"] = True


def main(argv: Sequence[str] | None = None) -> int:
    command = parser()
    try:
        args = command.parse_args(argv)
    except SystemExit as exc:
        return int(exc.code)
    selected_name: str | None = None
    raw_backup = args.backup or args.backup_positional
    if raw_backup:
        try:
            selected_name = Path(raw_backup).name
            if BACKUP_NAME_RE.fullmatch(selected_name) is None:
                selected_name = None
        except (TypeError, ValueError):
            selected_name = None
    report = initial_report(selected_name)
    try:
        run(args, report)
    except DrillError as exc:
        report["status"] = "failed"
        report["failure"] = {"code": exc.code, "stage": exc.stage}
    except Exception:
        # Never expose interpreter, filesystem, SQL, or candidate details in
        # a report.  The fixed code remains actionable without private data.
        report["status"] = "failed"
        report["failure"] = {"code": "internal_error", "stage": "runtime"}
    if args.report_file:
        try:
            write_report(ensure_absolute_path(args.report_file, "invalid_report_path"), report)
        except DrillError as exc:
            report["status"] = "failed"
            report["failure"] = {"code": exc.code, "stage": exc.stage}
    output = json.dumps(report, sort_keys=True, separators=(",", ":"))
    print(output)
    return 0 if report["status"] == "ok" else 1


if __name__ == "__main__":
    raise SystemExit(main())
