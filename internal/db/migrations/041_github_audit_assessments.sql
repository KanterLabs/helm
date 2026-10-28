-- Additive and nullable: legacy findings retain their original interpretation,
-- and retained binaries can continue using their named-column INSERT/SELECTs.
ALTER TABLE audit_findings ADD COLUMN assessment_json TEXT
 CHECK(assessment_json IS NULL OR (json_valid(assessment_json) AND COALESCE(json_type(assessment_json)='object' AND json_extract(assessment_json,'$.assessment_version')=2,0)));

-- A v2 assessment's exact destination/projection is immutable even through old
-- review clients. Review state/version remain mutable under existing guards.
CREATE TRIGGER audit_assessment_projection_immutable
BEFORE UPDATE OF proposed_semantic_destination,assessment_json ON audit_findings
WHEN OLD.assessment_json IS NOT NULL AND
 (NEW.assessment_json IS NOT OLD.assessment_json OR NEW.proposed_semantic_destination IS NOT OLD.proposed_semantic_destination)
BEGIN SELECT RAISE(ABORT,'versioned assessment destination is immutable'); END;
