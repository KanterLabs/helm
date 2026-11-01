package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// A source ID collision is reused only for an equal mapped record. Candidate
// lookup is deterministic across retries and never updates destination data.
func portableV3Resolve[T any](entity, source string, want T, plan *portableImportPlan, load func(string) (T, bool, error), withID func(T, string) T) (T, bool, error) {
	existing, occupied, err := load(source)
	if err != nil {
		return want, false, err
	}
	if occupied && reflect.DeepEqual(existing, want) {
		return want, false, nil
	}
	if occupied && plan.options.Conflict == portableConflictFail {
		return want, false, portableConflictError(plan.report, entity+" id conflict")
	}
	target := source
	if occupied {
		for attempt := 0; ; attempt++ {
			candidate := portableCandidateIDAt(entity, source, attempt)
			if candidate == source {
				continue
			}
			v, exists, err := load(candidate)
			if err != nil {
				return want, false, err
			}
			if !exists || reflect.DeepEqual(v, withID(want, candidate)) {
				target, occupied = candidate, exists
				break
			}
		}
		portableAddRemap(&plan.report, entity, source, target, "id", "source id conflicted with a destination record")
	}
	return withID(want, target), !occupied, nil
}

func planPortableGithub(ctx context.Context, q portableSQL, plan *portableImportPlan, actors map[string]string) error {
	if plan.archive.Github == nil {
		return nil
	}
	g := plan.archive.Github
	artifactMap := map[string]string{}
	runMap := map[string]string{}
	plan.github.digests = map[string]string{}
	plan.github.eventRefs = map[string]map[int]int64{}
	for _, source := range g.Artifacts {
		want := source
		want.ProjectID = plan.projectMap[source.ProjectID]
		// The same PR can already exist under another artifact ID. Resolve the
		// immutable repository identity before the human-readable repository path.
		existing, err := scanPortableArtifact(q.QueryRowContext(ctx, `SELECT `+portableArtifactColumns+` FROM github_artifacts a WHERE a.project_id=? AND a.number=? AND (a.repository=? COLLATE NOCASE OR (? IS NOT NULL AND a.repository_id=?)) ORDER BY a.id LIMIT 1`, want.ProjectID, want.Number, want.Repository, want.RepositoryID, want.RepositoryID))
		if err == nil {
			if existing.ID != source.ID {
				portableAddRemap(&plan.report, "artifact", source.ID, existing.ID, "id", "reused existing pull request identity")
			}
			artifactMap[source.ID] = existing.ID
			plan.github.artifacts = append(plan.github.artifacts, portableV3Record[PortableGithubArtifact]{value: existing})
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		load := func(id string) (PortableGithubArtifact, bool, error) {
			for _, r := range plan.github.artifacts {
				if r.value.ID == id {
					return r.value, true, nil
				}
			}
			v, e := scanPortableArtifact(q.QueryRowContext(ctx, `SELECT `+portableArtifactColumns+` FROM github_artifacts a WHERE a.id=?`, id))
			if errors.Is(e, sql.ErrNoRows) {
				return v, false, nil
			}
			return v, e == nil, e
		}
		mapped, create, err := portableV3Resolve("artifact", source.ID, want, plan, load, func(v PortableGithubArtifact, id string) PortableGithubArtifact { v.ID = id; return v })
		if err != nil {
			return err
		}
		artifactMap[source.ID] = mapped.ID
		plan.github.artifacts = append(plan.github.artifacts, portableV3Record[PortableGithubArtifact]{value: mapped, create: create})
	}
	for _, source := range g.TaskArtifacts {
		v := source
		v.TaskID = plan.taskMap[v.TaskID]
		v.ArtifactID = artifactMap[v.ArtifactID]
		actor, err := portableMapOptionalActor(q, v.CreatedBy, plan.options.ActorID, &plan.report, actors)
		if err != nil {
			return err
		}
		v.CreatedBy = actor
		var exists int
		if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_artifacts WHERE task_id=? AND artifact_id=?)`, v.TaskID, v.ArtifactID).Scan(&exists); err != nil {
			return err
		}
		plan.github.links = append(plan.github.links, portableV3Record[PortableTaskArtifact]{value: v, create: exists == 0})
	}
	for _, source := range g.AuditRuns {
		digest := portableV3AuditDigest(source, g)
		v := source
		v.ProjectID = plan.projectMap[v.ProjectID]
		actor, err := portableMapActor(q, v.ActorID, plan.options.ActorID, &plan.report, actors)
		if err != nil {
			return err
		}
		v.ActorID = actor
		if v.Status == "queued" || v.Status == "running" {
			v.Status = "partial"
			timestamp := v.UpdatedAt
			v.FinalizedAt = &timestamp
			plan.report.Warnings = append(plan.report.Warnings, "imported unfinished audit was retained as partial history; no job was launched")
		}
		load := func(id string) (PortableAuditRun, bool, error) {
			for _, r := range plan.github.runs {
				if r.value.ID == id {
					v := r.value
					if plan.github.digests[id] != digest {
						v.Scope = "\x00different-audit-history"
					}
					return v, true, nil
				}
			}
			existing, e := scanPortableAuditRun(q.QueryRowContext(ctx, `SELECT `+portableAuditRunColumns+` FROM audit_runs r WHERE r.id=?`, id))
			if errors.Is(e, sql.ErrNoRows) {
				return existing, false, nil
			}
			if e != nil {
				return existing, false, e
			}
			var savedDigest string
			e = q.QueryRowContext(ctx, `SELECT archive_digest FROM audit_imports WHERE audit_id=?`, id).Scan(&savedDigest)
			if errors.Is(e, sql.ErrNoRows) {
				e = nil
			}
			if savedDigest != digest {
				matches := false
				if savedDigest != "" {
					var historyErr error
					matches, historyErr = portableV3RunHistoryMatches(ctx, q, source, g, plan, id)
					if historyErr != nil {
						return existing, false, historyErr
					}
				}
				if !matches {
					existing.Scope = "\x00different-audit-history"
				}
			}
			return existing, true, e
		}
		mapped, create, err := portableV3Resolve("audit", source.ID, v, plan, load, func(v PortableAuditRun, id string) PortableAuditRun { v.ID = id; return v })
		if err != nil {
			return err
		}
		runMap[source.ID] = mapped.ID
		plan.github.digests[mapped.ID] = digest
		plan.github.runs = append(plan.github.runs, portableV3Record[PortableAuditRun]{value: mapped, create: create})
	}
	for _, source := range g.AuditFindings {
		v := source
		v.AuditID = runMap[v.AuditID]
		v.TaskID = plan.taskMap[v.TaskID]
		if mapped, ok := plan.columnMap[v.SourceColumn]; ok {
			v.SourceColumn = mapped
		}
		v.EvidenceRefs = append([]string{}, source.EvidenceRefs...)
		for i, ref := range v.EvidenceRefs {
			v.EvidenceRefs[i] = portableV3Reference(ref, plan, runMap)
		}
		load := func(id string) (PortableAuditFinding, bool, error) {
			for _, r := range plan.github.findings {
				if r.value.ID == id {
					return portableV3FindingWithoutLocalCursors(r.value, v), true, nil
				}
			}
			existing, e := scanPortableAuditFinding(q.QueryRowContext(ctx, `SELECT `+portableAuditFindingColumns+` FROM audit_findings f WHERE f.id=?`, id))
			if errors.Is(e, sql.ErrNoRows) {
				return existing, false, nil
			}
			return portableV3FindingWithoutLocalCursors(existing, v), e == nil, e
		}
		mapped, create, err := portableV3Resolve("audit_finding", source.ID, v, plan, load, func(v PortableAuditFinding, id string) PortableAuditFinding { v.ID = id; return v })
		if err != nil {
			return err
		}
		plan.github.eventRefs[mapped.ID] = map[int]int64{}
		for i, ref := range source.EvidenceRefs {
			if strings.HasPrefix(ref, "events:") {
				if n, err := strconv.ParseInt(strings.TrimPrefix(ref, "events:"), 10, 64); err == nil {
					plan.github.eventRefs[mapped.ID][i] = n
				}
			}
		}
		plan.github.findings = append(plan.github.findings, portableV3Record[PortableAuditFinding]{value: mapped, create: create})
	}
	for _, source := range g.AuditEvidence {
		// Deep copy slices so validation/import cannot mutate the caller's archive.
		raw, err := json.Marshal(source)
		if err != nil {
			return err
		}
		var e GithubAuditEvidence
		if err = json.Unmarshal(raw, &e); err != nil {
			return err
		}
		e.AuditID = runMap[e.AuditID]
		remapPortableV3EvidenceTasks(&e, plan)
		plan.github.evidence = append(plan.github.evidence, e)
	}
	plan.github.artifactMap = artifactMap
	plan.github.runMap = runMap
	plan.github.findingMap = map[string]string{}
	for i, record := range plan.github.findings {
		plan.github.findingMap[g.AuditFindings[i].ID] = record.value.ID
	}
	return nil
}

func portableV3Reference(ref string, plan *portableImportPlan, runs map[string]string) string {
	for namespace, mapping := range map[string]map[string]string{"tasks": plan.taskMap, "projects": plan.projectMap, "columns": plan.columnMap, "audits": runs, "comments": plan.commentMap} {
		for _, prefix := range []string{namespace + ":", namespace + "/", "/api/v1/" + namespace + "/"} {
			if strings.HasPrefix(ref, prefix) {
				id := strings.TrimPrefix(ref, prefix)
				end := strings.IndexByte(id, '/')
				suffix := ""
				if end >= 0 {
					suffix = id[end:]
					id = id[:end]
				}
				if mapped, ok := mapping[id]; ok {
					return prefix + mapped + suffix
				}
			}
		}
	}
	if strings.HasPrefix(ref, "events:") {
		n, err := strconv.ParseInt(strings.TrimPrefix(ref, "events:"), 10, 64)
		if err == nil {
			if mapped, ok := plan.eventCursorMap[n]; ok {
				return "events:" + strconv.FormatInt(mapped, 10)
			}
			imported := false
			for _, event := range plan.archive.Activity.Events {
				if event.Cursor == n {
					imported = true
					break
				}
			}
			if !imported {
				return "unavailable:events:" + strconv.FormatInt(n, 10)
			}
		}
	}
	return ref
}

// Fingerprinting the complete source subtree prevents an equal run header from
// silently merging different immutable findings or evidence into old history.
func portableV3AuditDigest(run PortableAuditRun, g *PortableGithubArchive) string {
	snapshot := struct {
		Run      PortableAuditRun
		Findings []PortableAuditFinding
		Evidence []GithubAuditEvidence
	}{Run: run}
	for _, f := range g.AuditFindings {
		if f.AuditID == run.ID {
			snapshot.Findings = append(snapshot.Findings, f)
		}
	}
	for _, e := range g.AuditEvidence {
		if e.AuditID == run.ID {
			snapshot.Evidence = append(snapshot.Evidence, e)
		}
	}
	sort.Slice(snapshot.Findings, func(i, j int) bool { return snapshot.Findings[i].ID < snapshot.Findings[j].ID })
	raw, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Local event cursors are assigned only during execution. The audit subtree
// digest already guarantees the original citation set is equal on a retry.
func portableV3FindingWithoutLocalCursors(existing, want PortableAuditFinding) PortableAuditFinding {
	existing.EvidenceRefs = append([]string{}, existing.EvidenceRefs...)
	for i, ref := range want.EvidenceRefs {
		if i < len(existing.EvidenceRefs) && (strings.HasPrefix(ref, "events:") || strings.HasPrefix(ref, "unavailable:events:")) {
			existing.EvidenceRefs[i] = ref
		}
	}
	return existing
}
