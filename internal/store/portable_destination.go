package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

func portableAddRemap(report *PortableImportReport, entity, source, target, field, reason string) {
	if source == target && field == "" {
		return
	}
	report.Remaps = append(report.Remaps, PortableImportRemap{Entity: entity, Source: source, Target: target, Field: field, Reason: reason})
}

func portableMapActor(q portableSQL, source, importer string, report *PortableImportReport, cache map[string]string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil
	}
	if target, ok := cache[source]; ok {
		return target, nil
	}
	var exists int
	if err := q.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM actors WHERE id=?)`, source).Scan(&exists); err != nil {
		return "", err
	}
	if exists != 0 {
		cache[source] = source
		return source, nil
	}
	cache[source] = importer
	portableAddRemap(report, "actor", source, importer, "id", "actor is not present on the destination; attributed to the importing actor")
	return importer, nil
}

func portableMapOptionalActor(q portableSQL, source *string, importer string, report *PortableImportReport, cache map[string]string) (*string, error) {
	if source == nil {
		return nil, nil
	}
	target, err := portableMapActor(q, *source, importer, report, cache)
	if err != nil {
		return nil, err
	}
	return &target, nil
}

func portableExistingNamedLabel(q portableSQL, projectID, name string) (PortableLabel, bool, error) {
	var label PortableLabel
	err := q.QueryRowContext(context.Background(), `SELECT id,project_id,name,color,created_at,updated_at FROM labels WHERE project_id=? AND lower(name)=lower(?) LIMIT 1`, projectID, name).Scan(&label.ID, &label.ProjectID, &label.Name, &label.Color, &label.CreatedAt, &label.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableLabel{}, false, nil
	}
	if err != nil {
		return PortableLabel{}, false, err
	}
	return label, true, nil
}

func portableExistingNamedRelease(q portableSQL, projectID, name string) (PortableRelease, bool, error) {
	var release PortableRelease
	var targetDate, releasedAt, releasedBy sql.NullString
	err := q.QueryRowContext(context.Background(), `SELECT id,project_id,name,description,target_date,released_at,released_by,version,created_at,updated_at FROM releases WHERE project_id=? AND lower(name)=lower(?) LIMIT 1`, projectID, name).Scan(&release.ID, &release.ProjectID, &release.Name, &release.Description, &targetDate, &releasedAt, &releasedBy, &release.Version, &release.CreatedAt, &release.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableRelease{}, false, nil
	}
	if err != nil {
		return PortableRelease{}, false, err
	}
	release.TargetDate, release.ReleasedAt, release.ReleasedBy = nullableString(targetDate), nullableString(releasedAt), nullableString(releasedBy)
	if release.ReleasedAt != nil {
		release.Status = "released"
	} else {
		release.Status = "planned"
	}
	return release, true, nil
}

func portableReleaseNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func portableProjectReleaseNameKey(projectID, name string) string {
	return projectID + "\x00" + portableReleaseNameKey(name)
}

func portableReleaseNameCandidate(base string, attempt int) string {
	suffix := " (imported)"
	if attempt > 0 {
		suffix = fmt.Sprintf(" (imported %d)", attempt+1)
	}
	return portableSuffix(base, suffix, 200)
}

// portableUniqueReleaseName keeps the source and destination releases
// separate when a same-project name collision has incompatible fields. The
// suffix sequence is deterministic so retries can discover the same imported
// release, while the used set also accounts for multiple releases planned in
// one archive.
func portableUniqueReleaseName(base, projectID string, used map[string]struct{}) string {
	for attempt := 0; attempt < 1000; attempt++ {
		candidate := portableReleaseNameCandidate(base, attempt)
		if _, exists := used[portableProjectReleaseNameKey(projectID, candidate)]; !exists {
			return candidate
		}
	}
	// portableSafeID/portableValidateArchive bound release names, so this is
	// only a defensive fallback for an unusually crowded destination.
	sum := sha256.Sum256([]byte(base))
	return portableSuffix(base, " (imported-"+hex.EncodeToString(sum[:])[:8]+")", 200)
}

// portableFindMatchingReleaseNameCandidate finds a previously imported copy
// whose deterministic name was selected after a same-name release conflict.
// It intentionally continues past occupied unrelated names: an earlier
// import may have had to skip one or more candidates before inserting its
// release.
func portableFindMatchingReleaseNameCandidate(q portableSQL, projectID string, source PortableRelease) (PortableRelease, bool, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		name := portableReleaseNameCandidate(source.Name, attempt)
		candidate, exists, err := portableExistingNamedRelease(q, projectID, name)
		if err != nil {
			return PortableRelease{}, false, err
		}
		if !exists {
			// A prior import would have selected the first free deterministic
			// name, so no later name can be its remapped copy.
			return PortableRelease{}, false, nil
		}
		expected := source
		expected.Name = name
		if portableReleaseFieldsEqual(candidate, expected) {
			return candidate, true, nil
		}
	}
	return PortableRelease{}, false, nil
}

func portableExistingColumnPosition(q portableSQL, projectID string, position int) (bool, error) {
	var exists int
	err := q.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM columns WHERE project_id=? AND position=?)`, projectID, position).Scan(&exists)
	return exists != 0, err
}

func portableExistingTaskNumber(q portableSQL, projectID string, number int) (bool, error) {
	var exists int
	err := q.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM tasks WHERE project_id=? AND number=?)`, projectID, number).Scan(&exists)
	return exists != 0, err
}

func portableNextColumnPosition(used map[int]struct{}) int {
	position := 0
	for {
		if _, exists := used[position]; !exists {
			return position
		}
		position++
	}
}

func portableNextTaskNumber(used map[int]struct{}) int {
	number := 1
	for {
		if _, exists := used[number]; !exists {
			return number
		}
		number++
	}
}

func portableTaskLabelExists(q portableSQL, taskID, labelID string) (bool, error) {
	var exists int
	err := q.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM task_labels WHERE task_id=? AND label_id=?)`, taskID, labelID).Scan(&exists)
	return exists != 0, err
}

func portableDependencyExists(q portableSQL, taskID, prerequisiteID string) (bool, error) {
	var exists int
	err := q.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM task_dependencies WHERE task_id=? AND prerequisite_task_id=?)`, taskID, prerequisiteID).Scan(&exists)
	return exists != 0, err
}

func portableExistingDependencyNode(ctx context.Context, q portableSQL, taskID string) (portableDependencyNode, bool, error) {
	var node portableDependencyNode
	var live, completed, claimActive int
	err := q.QueryRowContext(ctx, `SELECT t.id,t.project_id,t.deleted_at IS NULL,t.completed_at IS NOT NULL,COALESCE(c.semantic_state,''),CASE WHEN t.claimed_by IS NOT NULL AND t.claim_expires_at IS NOT NULL AND julianday(t.claim_expires_at)>julianday('now') THEN 1 ELSE 0 END FROM tasks t LEFT JOIN columns c ON c.id=t.column_id WHERE t.id=?`, taskID).Scan(&node.ID, &node.ProjectID, &live, &completed, &node.SemanticState, &claimActive)
	if errors.Is(err, sql.ErrNoRows) {
		return portableDependencyNode{}, false, nil
	}
	if err != nil {
		return portableDependencyNode{}, false, err
	}
	node.Live, node.Completed, node.ClaimActive = live != 0, completed != 0, claimActive != 0
	return node, true, nil
}

func portablePlannedDependencyNode(task portableTaskPlan, columnStates map[string]string) portableDependencyNode {
	claimActive := false
	if task.claimedBy != nil && task.claimExpiresAt != nil {
		if expires, err := time.Parse(time.RFC3339Nano, *task.claimExpiresAt); err == nil {
			claimActive = expires.After(time.Now().UTC())
		}
	}
	return portableDependencyNode{ID: task.id, ProjectID: task.projectID, SemanticState: columnStates[task.columnID], Live: true, Completed: task.source.CompletedAt != nil, ClaimActive: claimActive}
}

func portableDependencyGraphHasPath(graph map[string]map[string]struct{}, start, target string) bool {
	if start == target {
		return true
	}
	visited := map[string]struct{}{}
	stack := []string{start}
	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]
		if _, seen := visited[node]; seen {
			continue
		}
		visited[node] = struct{}{}
		for next := range graph[node] {
			if next == target {
				return true
			}
			if _, seen := visited[next]; !seen {
				stack = append(stack, next)
			}
		}
	}
	return false
}

func portableLinkExists(q portableSQL, source, target, linkType string) (bool, error) {
	var exists int
	err := q.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM task_links WHERE source_task_id=? AND target_task_id=? AND link_type=?)`, source, target, linkType).Scan(&exists)
	return exists != 0, err
}

// portableExistingLinkTarget checks the actual uniqueness boundary enforced by
// task_links_source_type_idx. Checking the full source/target/type triple is
// insufficient: a different target with the same source and type still makes
// the INSERT fail, so imports must surface that conflict during preflight.
func portableExistingLinkTarget(q portableSQL, source, linkType string) (string, bool, error) {
	var target string
	err := q.QueryRowContext(context.Background(), `SELECT target_task_id FROM task_links WHERE source_task_id=? AND link_type=?`, source, linkType).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return target, true, nil
}
