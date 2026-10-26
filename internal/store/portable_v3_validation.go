package store

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

func validatePortableGithub(archive *PortableArchive, report *PortableImportReport, projects map[string]PortableProject, tasks map[string]PortableTask) {
	g := archive.Github
	if g == nil {
		return
	}
	issue := func(entity, id, field, message string) { addPortableIssue(report, entity, id, field, message) }
	if archive.Version != PortableVersionGithub {
		issue("github", "", "version", "github records require portable version 3")
		return
	}
	if len(g.Artifacts) > 50000 || len(g.TaskArtifacts) > 100000 || len(g.AuditRuns) > 10000 || len(g.AuditFindings) > 100000 || len(g.AuditEvidence) > 10000 {
		issue("github", "", "records", "too many retained records")
		return
	}
	artifacts := map[string]PortableGithubArtifact{}
	identities := map[string]bool{}
	for _, a := range g.Artifacts {
		identity, err := githubprovider.ParsePullRequestURL(a.URL)
		if err != nil || identity.Repository != a.Repository || identity.Number != a.Number || identity.URL != a.URL {
			issue("artifact", a.ID, "url", "artifact must contain a canonical GitHub pull request identity")
		}
		if !portableSafeID(a.ID) || !portableTimestamp(a.CreatedAt, false) || a.Number <= 0 || (a.RepositoryID != nil && *a.RepositoryID <= 0) {
			issue("artifact", a.ID, "identity", "invalid artifact identity or timestamp")
		}
		if _, ok := projects[a.ProjectID]; !ok {
			issue("artifact", a.ID, "project_id", "project does not exist in archive")
		}
		if _, ok := artifacts[a.ID]; ok {
			issue("artifact", a.ID, "id", "duplicate artifact id")
		}
		artifacts[a.ID] = a
		key := fmt.Sprintf("%s/%s/%d", a.ProjectID, strings.ToLower(a.Repository), a.Number)
		if identities[key] {
			issue("artifact", a.ID, "repository", "duplicate artifact identity")
		}
		identities[key] = true
		if a.RepositoryID != nil {
			key = fmt.Sprintf("%s/%d/%d", a.ProjectID, *a.RepositoryID, a.Number)
			if identities[key] {
				issue("artifact", a.ID, "repository_id", "duplicate immutable artifact identity")
			}
			identities[key] = true
		}
	}
	links := map[string]bool{}
	for _, link := range g.TaskArtifacts {
		task, taskOK := tasks[link.TaskID]
		artifact, artifactOK := artifacts[link.ArtifactID]
		if !taskOK || !artifactOK || task.ProjectID != artifact.ProjectID {
			issue("task_artifact", link.TaskID, "artifact_id", "task and artifact must exist in one archive project")
		}
		key := link.TaskID + "/" + link.ArtifactID
		if links[key] {
			issue("task_artifact", link.TaskID, "artifact_id", "duplicate task artifact")
		}
		links[key] = true
		if !portableTimestamp(link.CreatedAt, false) || (link.CreatedBy != nil && !portableSafeID(*link.CreatedBy)) {
			issue("task_artifact", link.TaskID, "created_at", "invalid relationship timestamp or actor")
		}
	}
	runs := map[string]PortableAuditRun{}
	for _, run := range g.AuditRuns {
		if _, ok := runs[run.ID]; ok {
			issue("audit", run.ID, "id", "duplicate audit id")
		}
		runs[run.ID] = run
		if !portableSafeID(run.ID) || !portableSafeID(run.ActorID) {
			issue("audit", run.ID, "id", "invalid audit or actor id")
		}
		if _, ok := projects[run.ProjectID]; !ok {
			issue("audit", run.ID, "project_id", "project does not exist in archive")
		}
		if _, err := validateAuditRunInput(AuditRunInput{Scope: run.Scope}); err != nil {
			issue("audit", run.ID, "scope", "invalid audit scope")
		}
		terminal := run.Status == "complete" || run.Status == "partial" || run.Status == "failed"
		if (!terminal && run.Status != "queued" && run.Status != "running") || terminal != (run.FinalizedAt != nil) {
			issue("audit", run.ID, "status", "invalid audit finalization state")
		}
		if !portableTimestamp(run.StartedAt, false) || !portableTimestamp(run.CreatedAt, false) || !portableTimestamp(run.UpdatedAt, false) || (run.FinalizedAt != nil && !portableTimestamp(*run.FinalizedAt, false)) {
			issue("audit", run.ID, "timestamp", "timestamps must be RFC3339")
		}
	}
	findings := map[string]bool{}
	runTasks := map[string]bool{}
	for _, finding := range g.AuditFindings {
		run, runOK := runs[finding.AuditID]
		task, taskOK := tasks[finding.TaskID]
		if !runOK || !taskOK || run.ProjectID != task.ProjectID {
			issue("audit_finding", finding.ID, "task_id", "finding task must exist in the audit project")
		}
		input := AuditFindingInput{TaskID: finding.TaskID, CapturedVersion: finding.CapturedVersion, SourceColumn: finding.SourceColumn, Verdict: finding.Verdict, ProposedSemanticDestination: finding.ProposedSemanticDestination, Confidence: finding.Confidence, Reason: finding.Reason, EvidenceRefs: finding.EvidenceRefs}
		if _, err := validateAuditFindingInput(input); err != nil {
			issue("audit_finding", finding.ID, "snapshot", "invalid finding snapshot or citations")
		}
		if !portableSafeID(finding.ID) || findings[finding.ID] || finding.Version <= 0 || !validAuditReviewState(finding.ReviewState) {
			issue("audit_finding", finding.ID, "id", "invalid or duplicate finding")
		}
		findings[finding.ID] = true
		key := finding.AuditID + "/" + finding.TaskID
		if runTasks[key] {
			issue("audit_finding", finding.ID, "task_id", "duplicate finding task in audit")
		}
		runTasks[key] = true
		if !portableTimestamp(finding.CreatedAt, false) || !portableTimestamp(finding.UpdatedAt, false) {
			issue("audit_finding", finding.ID, "timestamp", "timestamps must be RFC3339")
		}
	}
	manifests := map[string]bool{}
	for _, e := range g.AuditEvidence {
		run, ok := runs[e.AuditID]
		if !ok || manifests[e.AuditID] {
			issue("audit_evidence", e.AuditID, "audit_id", "missing or duplicate audit manifest")
		}
		manifests[e.AuditID] = true
		if !portableTimestamp(e.CapturedAt, false) || len(e.Items) > 10000 || len(e.Tasks) > 10000 || len(e.Repositories) > 100 || len(e.Omissions) > 10000 {
			issue("audit_evidence", e.AuditID, "manifest", "invalid or oversized evidence manifest")
		}
		raw, err := json.Marshal(e)
		if err != nil || len(raw) > 8*1024*1024 {
			issue("audit_evidence", e.AuditID, "manifest", "evidence manifest exceeds 8 MiB")
		}
		seen := map[string]bool{}
		capturedTasks := map[string]bool{}
		for _, task := range e.Tasks {
			capturedTasks[task.ID] = true
			t, ok := tasks[task.ID]
			if !ok || t.ProjectID != run.ProjectID || task.Version <= 0 || len(task.Title) > 200 || len(task.Description) > 20000 {
				issue("audit_evidence", e.AuditID, "tasks", "captured task is not in the audit project or is invalid")
			}
		}
		for _, item := range e.Items {
			associated := map[string]bool{}
			if len(item.TaskIDs) > 10000 {
				issue("audit_evidence", e.AuditID, "task_ids", "too many evidence task associations")
			}
			for _, taskID := range item.TaskIDs {
				task, exists := tasks[taskID]
				if !exists || !capturedTasks[taskID] || task.ProjectID != run.ProjectID || associated[taskID] {
					issue("audit_evidence", e.AuditID, "task_ids", "evidence task associations must be unique captured tasks in the audit project")
				}
				associated[taskID] = true
			}
			parsed, err := url.Parse(item.URL)
			if !portableSafeID(item.ID) || seen[item.ID] || !auditSafeRefPattern.MatchString(item.ID) || len(item.Text) > 1024*1024 || len(item.Title) > 2000 || err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || len(item.SHA) != 40 || (item.Kind != "file" && item.Kind != "pull_request" && item.Kind != "check") {
				issue("audit_evidence", e.AuditID, "items", "invalid retained evidence item")
			}
			seen[item.ID] = true
		}
	}
}
