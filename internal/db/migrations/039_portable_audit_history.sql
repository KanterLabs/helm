-- Imported audit history is retained separately from operational jobs. An
-- imported run cannot gain an executable lease or live-source freshness.
CREATE TABLE audit_imports (
 audit_id TEXT PRIMARY KEY REFERENCES audit_runs(id) ON DELETE CASCADE,
 imported_at TEXT NOT NULL,
 archive_digest TEXT NOT NULL DEFAULT ''
);
CREATE TABLE github_audit_retained_evidence (
 audit_id TEXT PRIMARY KEY REFERENCES audit_runs(id) ON DELETE CASCADE,
 evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json))
);
-- Keep imported history read-only for retained binaries that do not know the
-- provenance tables. Import inserts the marker after the complete subtree.
CREATE TRIGGER imported_audit_finding_insert_guard
BEFORE INSERT ON audit_findings
WHEN EXISTS(SELECT 1 FROM audit_imports WHERE audit_id=NEW.audit_id)
BEGIN SELECT RAISE(ABORT,'imported audit history is read only'); END;
CREATE TRIGGER imported_audit_finding_update_guard
BEFORE UPDATE ON audit_findings
WHEN EXISTS(SELECT 1 FROM audit_imports WHERE audit_id=OLD.audit_id)
BEGIN SELECT RAISE(ABORT,'imported audit history is read only'); END;
CREATE TRIGGER imported_audit_status_guard
BEFORE UPDATE OF status ON audit_runs
WHEN EXISTS(SELECT 1 FROM audit_imports WHERE audit_id=OLD.id)
BEGIN SELECT RAISE(ABORT,'imported audit history is read only'); END;
