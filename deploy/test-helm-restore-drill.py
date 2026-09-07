#!/usr/bin/env python3
"""Fixture coverage for the isolated TC-163 restore drill."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import stat
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
DRILL = ROOT / "deploy" / "helm-restore-drill.py"
DRILL_SPEC = importlib.util.spec_from_file_location("helm_restore_drill_fixture_module", DRILL)
if DRILL_SPEC is None or DRILL_SPEC.loader is None:
    raise RuntimeError("could not load restore drill module")
DRILL_MODULE = importlib.util.module_from_spec(DRILL_SPEC)
DRILL_SPEC.loader.exec_module(DRILL_MODULE)


class RestoreDrillFixtures(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="helm-restore-drill-test.")
        self.root = Path(self.temp.name)
        self.backups = self.root / "backups"
        self.backups.mkdir(mode=0o700)

    def tearDown(self) -> None:
        self.temp.cleanup()

    def create_backup(self, *, name: str = "roadmap-20260907T200000Z-manual.db") -> Path:
        backup = self.backups / name
        connection = sqlite3.connect(backup)
        connection.executescript(
            """
            PRAGMA foreign_keys=ON;
            CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
            INSERT INTO schema_migrations VALUES (1, '2026-09-07T20:00:00Z');
            CREATE TABLE actors(id TEXT PRIMARY KEY, name TEXT NOT NULL);
            CREATE TABLE projects(id TEXT PRIMARY KEY, name TEXT NOT NULL);
            CREATE TABLE tasks(
                id TEXT PRIMARY KEY,
                project_id TEXT NOT NULL REFERENCES projects(id),
                assignee_id TEXT REFERENCES actors(id),
                title TEXT NOT NULL
            );
            CREATE TABLE comments(
                id TEXT PRIMARY KEY,
                task_id TEXT NOT NULL REFERENCES tasks(id),
                actor_id TEXT NOT NULL REFERENCES actors(id),
                body TEXT NOT NULL
            );
            INSERT INTO actors VALUES ('actor-1', 'Agent');
            INSERT INTO actors VALUES ('actor-2', 'Human');
            INSERT INTO projects VALUES ('project-1', 'Recovery fixture');
            INSERT INTO tasks VALUES ('task-1', 'project-1', 'actor-1', 'Preserve this task');
            INSERT INTO tasks VALUES ('task-2', 'project-1', NULL, 'Preserve this task too');
            INSERT INTO comments VALUES ('comment-1', 'task-1', 'actor-2', 'Populated relationship');
            """
        )
        connection.commit()
        connection.close()
        os.chmod(backup, 0o600)
        checksum = hashlib.sha256(backup.read_bytes()).hexdigest()
        (backup.with_name(backup.name + ".sha256")).write_text(
            f"{checksum}  {backup.name}\n", encoding="ascii"
        )
        (backup.with_name(backup.name + ".metadata")).write_text(
            "release_sha=manual\n"
            "created_at=2026-09-07T20:00:00Z\n"
            "schema_version=1\n"
            "migration_digest=" + "0" * 64 + "\n",
            encoding="ascii",
        )
        os.chmod(backup.with_name(backup.name + ".sha256"), 0o600)
        os.chmod(backup.with_name(backup.name + ".metadata"), 0o600)
        return backup

    def run_drill(self, backup: Path, *extra: str) -> tuple[int, dict]:
        isolated = self.root / ("isolated-" + str(len(list(self.root.iterdir()))))
        command = [sys.executable, str(DRILL), "--backup", str(backup), "--isolated-dir", str(isolated)]
        command.extend(extra)
        completed = subprocess.run(command, text=True, capture_output=True, check=False)
        self.assertEqual(completed.stderr, "", completed.stderr)
        self.assertEqual(len(completed.stdout.splitlines()), 1, completed.stdout)
        return completed.returncode, json.loads(completed.stdout)

    def assert_source_unchanged(self, backup: Path, before: bytes, before_stat: os.stat_result) -> None:
        self.assertEqual(backup.read_bytes(), before)
        after_stat = backup.stat()
        self.assertEqual((after_stat.st_dev, after_stat.st_ino, after_stat.st_size, after_stat.st_mtime_ns),
                         (before_stat.st_dev, before_stat.st_ino, before_stat.st_size, before_stat.st_mtime_ns))

    def test_populated_backup_passes_and_reports_counts_relationships_and_identities(self) -> None:
        backup = self.create_backup()
        before = backup.read_bytes()
        before_stat = backup.stat()
        status, report = self.run_drill(backup)
        self.assertEqual(status, 0)
        self.assertEqual(report["status"], "ok")
        self.assertEqual(report["backup"]["checksum"], "valid")
        self.assertEqual(report["backup"]["metadata"], "valid")
        self.assertEqual(report["checks"]["source"]["row_counts"]["projects"], 1)
        self.assertEqual(report["checks"]["source"]["row_counts"]["tasks"], 2)
        self.assertEqual(report["checks"]["relationships"]["status"], "ok")
        self.assertEqual(report["checks"]["stable_identities"]["status"], "ok")
        self.assertTrue(report["source"]["unchanged"])
        self.assertFalse(report["isolation"]["live_database_touched"])
        self.assertFalse(report["isolation"]["services_managed"])
        self.assertFalse(report["isolation"]["credentials_revoked"])
        self.assertEqual(report["checks"]["stable_identities"]["schema_migrations"], "unchanged")
        self.assertEqual(report["external_compatibility"]["status"], "not_run")
        self.assert_source_unchanged(backup, before, before_stat)

    def test_new_report_file_is_published_without_overwrite(self) -> None:
        backup = self.create_backup()
        report_path = self.root / "new-report.json"
        isolated = self.root / "isolated-report-success"
        completed = subprocess.run(
            [
                sys.executable,
                str(DRILL),
                "--backup",
                str(backup),
                "--isolated-dir",
                str(isolated),
                "--report",
                str(report_path),
            ],
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(completed.returncode, 0, completed.stdout)
        self.assertEqual(completed.stderr, "")
        self.assertEqual(report_path.read_bytes(), completed.stdout.encode("utf-8"))
        self.assertEqual(stat.S_IMODE(report_path.stat().st_mode), 0o600)
        self.assertEqual(json.loads(report_path.read_text()), json.loads(completed.stdout))

    def test_automatic_isolated_directory_is_cleaned_after_success(self) -> None:
        backup = self.create_backup()
        before_entries = set(self.root.iterdir())
        completed = subprocess.run(
            [
                sys.executable,
                str(DRILL),
                "--backup",
                str(backup),
                "--workspace-dir",
                str(self.root),
            ],
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(completed.returncode, 0, completed.stdout)
        report = json.loads(completed.stdout)
        self.assertFalse(report["isolation"]["retained"])
        self.assertEqual(set(self.root.iterdir()), before_entries)

    def test_checksum_mismatch_fails_without_source_change(self) -> None:
        backup = self.create_backup()
        before = backup.read_bytes()
        before_stat = backup.stat()
        (backup.with_name(backup.name + ".sha256")).write_text(
            "f" * 64 + "  " + backup.name + "\n", encoding="ascii"
        )
        status, report = self.run_drill(backup)
        self.assertNotEqual(status, 0)
        self.assertEqual(report["failure"]["code"], "checksum_mismatch")
        self.assertEqual(report["backup"]["checksum"], "mismatch")
        self.assertTrue(report["source"]["unchanged"])
        self.assert_source_unchanged(backup, before, before_stat)

    def test_existing_report_path_is_never_overwritten(self) -> None:
        backup = self.create_backup()
        report_path = self.root / "existing-report.json"
        report_path.write_bytes(b"operator report\n")
        os.chmod(report_path, 0o600)
        before = report_path.read_bytes()
        command = [
            sys.executable,
            str(DRILL),
            "--backup",
            str(backup),
            "--isolated-dir",
            str(self.root / "isolated-report"),
            "--report",
            str(report_path),
        ]
        completed = subprocess.run(command, text=True, capture_output=True, check=False)
        self.assertNotEqual(completed.returncode, 0)
        report = json.loads(completed.stdout)
        self.assertEqual(report["failure"]["code"], "report_path_exists")
        self.assertEqual(report_path.read_bytes(), before)

    def test_report_cannot_target_selected_backup_or_sidecar(self) -> None:
        backup = self.create_backup()
        for target in (backup, backup.with_name(backup.name + ".sha256"), backup.with_name(backup.name + ".metadata")):
            completed = subprocess.run(
                [sys.executable, str(DRILL), "--backup", str(backup), "--report", str(target)],
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(completed.returncode, 0)
            report = json.loads(completed.stdout)
            self.assertEqual(report["failure"]["code"], "report_path_exists")

    def test_malformed_metadata_fails_closed(self) -> None:
        backup = self.create_backup()
        before = backup.read_bytes()
        before_stat = backup.stat()
        metadata = backup.with_name(backup.name + ".metadata")
        metadata.write_text(
            "release_sha=manual\nrelease_sha=manual\ncreated_at=2026-09-07T20:00:00Z\n",
            encoding="ascii",
        )
        status, report = self.run_drill(backup)
        self.assertNotEqual(status, 0)
        self.assertEqual(report["failure"]["code"], "metadata_malformed")
        self.assertTrue(report["source"]["unchanged"])
        self.assert_source_unchanged(backup, before, before_stat)

    def rewrite_checksum(self, backup: Path) -> None:
        checksum = hashlib.sha256(backup.read_bytes()).hexdigest()
        (backup.with_name(backup.name + ".sha256")).write_text(
            f"{checksum}  {backup.name}\n", encoding="ascii"
        )

    def test_integrity_failure_is_rejected_even_with_matching_checksum(self) -> None:
        backup = self.create_backup()
        connection = sqlite3.connect(backup)
        connection.execute("PRAGMA writable_schema=ON")
        connection.execute("UPDATE sqlite_master SET rootpage=999999 WHERE name='tasks'")
        connection.commit()
        connection.close()
        self.rewrite_checksum(backup)
        status, report = self.run_drill(backup)
        self.assertNotEqual(status, 0)
        self.assertIn(report["failure"]["code"], {"database_open_failed", "integrity_check_failed", "database_query_failed"})

    def test_foreign_key_failure_is_rejected_even_with_matching_checksum(self) -> None:
        backup = self.create_backup()
        connection = sqlite3.connect(backup)
        connection.execute("PRAGMA foreign_keys=OFF")
        connection.execute("INSERT INTO tasks VALUES ('orphan', 'missing-project', NULL, 'bad relationship')")
        connection.commit()
        connection.close()
        self.rewrite_checksum(backup)
        status, report = self.run_drill(backup)
        self.assertNotEqual(status, 0)
        self.assertEqual(report["failure"]["code"], "foreign_key_check_failed")

    def test_schema_migration_rows_are_preserved_not_only_versions(self) -> None:
        backup = self.create_backup()
        altered = self.root / "altered.db"
        altered.write_bytes(backup.read_bytes())
        os.chmod(altered, 0o600)
        connection = sqlite3.connect(altered)
        connection.execute(
            "UPDATE schema_migrations SET applied_at='2026-09-07T21:00:00Z' WHERE version=1"
        )
        connection.commit()
        connection.close()
        source_snapshot, _ = DRILL_MODULE.inspect_database(backup)
        altered_snapshot, _ = DRILL_MODULE.inspect_database(altered)
        with self.assertRaises(DRILL_MODULE.DrillError) as context:
            DRILL_MODULE.compare_snapshots(source_snapshot, altered_snapshot)
        self.assertEqual(context.exception.code, "schema_migrations_changed")

    def test_final_source_guard_drift_cannot_leave_success_report(self) -> None:
        backup = self.create_backup()
        isolated = self.root / "isolated-final-drift"
        args = DRILL_MODULE.parser().parse_args(
            ["--backup", str(backup), "--isolated-dir", str(isolated)]
        )
        report = DRILL_MODULE.initial_report(backup.name)
        original_unchanged = DRILL_MODULE.SourceGuard.unchanged
        calls = 0

        def drift_after_first_check(source_guard):
            nonlocal calls
            calls += 1
            if calls >= 2:
                return False
            return original_unchanged(source_guard)

        DRILL_MODULE.SourceGuard.unchanged = drift_after_first_check
        try:
            DRILL_MODULE.run(args, report)
        finally:
            DRILL_MODULE.SourceGuard.unchanged = original_unchanged
        self.assertEqual(report["status"], "failed")
        self.assertEqual(report["failure"]["code"], "source_changed")
        self.assertFalse(report["source"]["unchanged"])
        self.assertFalse(isolated.exists())

    def test_cleanup_failure_is_failed_and_marks_retained(self) -> None:
        backup = self.create_backup()
        args = DRILL_MODULE.parser().parse_args(
            ["--backup", str(backup), "--workspace-dir", str(self.root)]
        )
        report = DRILL_MODULE.initial_report(backup.name)
        original_remove = DRILL_MODULE.safe_remove_isolated
        DRILL_MODULE.safe_remove_isolated = lambda path: False
        try:
            with self.assertRaises(DRILL_MODULE.DrillError) as context:
                DRILL_MODULE.run(args, report)
        finally:
            DRILL_MODULE.safe_remove_isolated = original_remove
        self.assertEqual(context.exception.code, "isolated_cleanup_failed")
        self.assertEqual(report["status"], "failed")
        self.assertTrue(report["isolation"]["retained"])

    def test_candidate_execution_is_deferred_without_touching_source(self) -> None:
        backup = self.create_backup()
        before = backup.read_bytes()
        before_stat = backup.stat()
        marker = self.root / "candidate-ran"
        candidate = self.root / "candidate-starts"
        candidate.write_text(
            f"#!/bin/sh\ntouch '{marker}'\nexit 0\n",
            encoding="ascii",
        )
        os.chmod(candidate, 0o755)
        status, report = self.run_drill(backup, "--candidate-binary", str(candidate))
        self.assertNotEqual(status, 0)
        self.assertEqual(report["failure"]["code"], "external_compatibility_deferred")
        self.assertEqual(report["external_compatibility"]["status"], "not_run")
        self.assertFalse(marker.exists())
        self.assertTrue(report["source"]["unchanged"])
        self.assert_source_unchanged(backup, before, before_stat)

    def test_source_and_sidecars_must_not_be_symlinks(self) -> None:
        backup = self.create_backup()
        real = backup.with_name("real-sha256")
        checksum = backup.with_name(backup.name + ".sha256")
        real.write_bytes(checksum.read_bytes())
        os.chmod(real, 0o600)
        checksum.unlink()
        checksum.symlink_to(real)
        status, report = self.run_drill(backup)
        self.assertNotEqual(status, 0)
        self.assertEqual(report["failure"]["code"], "symlink_artifact")

    def test_wal_companion_entries_are_ignored_and_unchanged(self) -> None:
        backup = self.create_backup()
        wal = backup.with_name(backup.name + "-wal")
        shm = backup.with_name(backup.name + "-shm")
        wal.write_bytes(b"SQLite format 3\x00" + bytes(100))
        shm.write_bytes(bytes(128))
        os.chmod(wal, 0o600)
        os.chmod(shm, 0o600)
        before = {entry.name: entry.read_bytes() for entry in self.backups.iterdir()}
        status, report = self.run_drill(backup)
        self.assertEqual(status, 0)
        self.assertEqual(report["status"], "ok")
        after = {entry.name: entry.read_bytes() for entry in self.backups.iterdir()}
        self.assertEqual(after, before)

    def test_path_traversal_is_rejected_before_opening_backup(self) -> None:
        backup = self.create_backup()
        traversal = str(self.backups / ".." / "backups" / backup.name)
        completed = subprocess.run(
            [sys.executable, str(DRILL), "--backup", traversal],
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertNotEqual(completed.returncode, 0)
        report = json.loads(completed.stdout)
        self.assertEqual(report["failure"]["code"], "invalid_path")


if __name__ == "__main__":
    unittest.main()
