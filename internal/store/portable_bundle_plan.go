package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/KanterLabs/helm/internal/attachments"
)

type portableBundlePlan struct {
	entry    PortableBundleAttachment
	prepared *attachments.Prepared
	create   bool
}

func bundleStamp(value string) bool { _, e := time.Parse(time.RFC3339Nano, value); return e == nil }
func planPortableBundle(ctx context.Context, q portableSQL, root string, manifest PortableBundleManifest, prepared []*attachments.Prepared, plan *portableImportPlan, quota int64) ([]portableBundlePlan, error) {
	out := []portableBundlePlan{}
	taskProjects := map[string]string{}
	comments := map[string]string{}
	history := map[string]string{}
	historyIDs := map[string]string{}
	for _, t := range plan.archive.Tasks {
		taskProjects[t.ID] = t.ProjectID
	}
	for _, c := range plan.archive.Comments {
		comments[c.ID] = c.TaskID
	}
	for _, h := range plan.history {
		history[h.source.ID] = h.source.TaskID
		historyIDs[h.source.ID] = h.id
	}
	actorMap := map[string]string{}
	used, err := portableIDSet(q, "task_attachments")
	if err != nil {
		return nil, err
	}
	usedBytes := map[string]int64{}
	publications := map[string]int{}
	positions := map[string]map[int]bool{}
	for i, entry := range manifest.Attachments {
		a := entry.Attachment
		a.StoredBytes = prepared[i].StoredBytes
		if taskProjects[a.TaskID] == "" || taskProjects[a.TaskID] != a.ProjectID || plan.taskMap[a.TaskID] == "" || plan.projectMap[a.ProjectID] == "" {
			return nil, bundleInvalid("attachment task/project is outside archive")
		}
		if (entry.CommentID == "") == (entry.ProgressID == "") || !bundleStamp(entry.LinkedAt) || !bundleStamp(a.CreatedAt) || !bundleStamp(a.UpdatedAt) {
			return nil, bundleInvalid("invalid attachment publication")
		}
		source := "c:" + entry.CommentID
		if entry.CommentID != "" {
			if comments[entry.CommentID] != a.TaskID {
				return nil, bundleInvalid("comment attachment belongs to another task")
			}
			entry.CommentID = plan.commentMap[entry.CommentID]
		} else {
			if history[entry.ProgressID] != a.TaskID {
				return nil, bundleInvalid("progress attachment belongs to another task")
			}
			source = "p:" + entry.ProgressID
			entry.ProgressID = historyIDs[entry.ProgressID]
		}
		if positions[source] == nil {
			positions[source] = map[int]bool{}
		}
		if positions[source][entry.Position] {
			return nil, bundleInvalid("duplicate image position")
		}
		positions[source][entry.Position] = true
		publications[source]++
		if publications[source] > 4 {
			return nil, bundleInvalid("too many images per publication")
		}
		sourceID := a.ID
		a.TaskID = plan.taskMap[a.TaskID]
		a.ProjectID = plan.projectMap[a.ProjectID]
		a.ActorID, err = portableMapActor(q, a.ActorID, plan.options.ActorID, &plan.report, actorMap)
		if err != nil {
			return nil, err
		}
		entry.Attachment = a
		inspect := func(id string) (bool, bool, error) {
			existing, e := attachmentFromRow(q.QueryRowContext(ctx, `SELECT `+attachmentColumns+` FROM task_attachments a WHERE a.id=?`, id))
			if errors.Is(e, sql.ErrNoRows) {
				return false, false, nil
			}
			if e != nil {
				return false, false, e
			}
			mapped := a
			mapped.ID = id
			// Retained footprint is local derived storage, not portable metadata.
			existing.StoredBytes = 0
			mapped.StoredBytes = 0
			if !reflect.DeepEqual(existing, mapped) {
				return true, false, nil
			}
			var comment, progress, linkedAt string
			var ready, position int
			e = q.QueryRowContext(ctx, `SELECT COALESCE(l.comment_id,''),COALESCE(l.progress_id,''),COALESCE(l.created_at,''),a.ready,COALESCE(l.position,0) FROM task_attachments a LEFT JOIN task_attachment_links l ON l.attachment_id=a.id WHERE a.id=?`, id).Scan(&comment, &progress, &linkedAt, &ready, &position)
			return true, e == nil && ready == 1 && comment == entry.CommentID && progress == entry.ProgressID && linkedAt == entry.LinkedAt && position == entry.Position, e
		}
		create := true
		var e error
		// Walk DB and filesystem conflicts together: an interrupted import may
		// leave an orphan at any candidate. Replays must find the matching row
		// after such an orphan instead of creating another published copy.
		for attempt := 0; ; attempt++ {
			if attempt > 1000 {
				return nil, bundleInvalid("too many conflicting objects")
			}
			if attempt > 0 {
				a.ID = portableCandidateIDAt("attachment", sourceID+":"+a.TaskID, attempt-1)
			}
			occupied, matching, lookupErr := inspect(a.ID)
			if lookupErr != nil {
				return nil, lookupErr
			}
			if matching {
				create = false
				break
			}
			_, reserved := used[a.ID]
			if !occupied && !reserved {
				path, pathErr := attachments.ObjectPath(root, a.ID, false)
				if pathErr != nil {
					return nil, pathErr
				}
				_, pathErr = os.Lstat(filepath.Dir(path))
				if os.IsNotExist(pathErr) {
					break
				}
				if pathErr != nil {
					return nil, pathErr
				}
			}
			if plan.options.Conflict == portableConflictFail {
				return nil, &Error{Kind: ErrConflict, Message: "attachment id or object conflict"}
			}
		}
		if create {
			if _, ok := usedBytes[a.ProjectID]; !ok {
				var retained int64
				if e = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN stored_bytes>0 THEN stored_bytes ELSE size_bytes END),0) FROM task_attachments WHERE project_id=?`, a.ProjectID).Scan(&retained); e != nil {
					return nil, e
				}
				usedBytes[a.ProjectID] = retained
			}
			if quota < a.StoredBytes || usedBytes[a.ProjectID] > quota-a.StoredBytes {
				return nil, &Error{Kind: ErrConflict, Message: "project attachment storage quota exceeded"}
			}
			usedBytes[a.ProjectID] += a.StoredBytes
		} else {
			// Replays also verify the installed immutable original before reuse.
			path, e := attachments.ObjectPath(root, a.ID, false)
			if e != nil {
				return nil, e
			}
			data, e := os.ReadFile(path)
			if e != nil || int64(len(data)) != a.SizeBytes || bundleHash(data) != a.SHA256 {
				return nil, bundleInvalid("existing attachment object is unavailable or corrupt")
			}
		}
		used[a.ID] = struct{}{}
		if sourceID != a.ID {
			portableAddRemap(&plan.report, "attachment", sourceID, a.ID, "id", "source id conflicted with destination attachment or object")
		}
		entry.Attachment = a
		out = append(out, portableBundlePlan{entry: entry, prepared: prepared[i], create: create})
	}
	return out, nil
}
