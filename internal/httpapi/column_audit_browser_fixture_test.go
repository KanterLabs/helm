package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/KanterLabs/helm/internal/store"
)

// Synthetic GitHub and model boundaries for the TC-310 browser harness. Every
// other layer (router, auth, store, worker, correction lifecycle) is real.
const (
	columnAuditRepository = "example/synthetic"
	columnAuditBaseSHA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	columnAuditAdvanceSHA = "dddddddddddddddddddddddddddddddddddddddd"
	columnAuditPRHeadSHA  = "cccccccccccccccccccccccccccccccccccccccc"
	columnAuditBlobSHA    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	columnAuditToken      = "SYNTHETIC_COLUMN_AUDIT_TOKEN"
)

// The source text is shown in the evidence panel, so it includes markup that
// must render literally rather than execute.
var columnAuditFiles = map[string]string{
	"internal/deps.go":     "package app\n\n// Prerequisites must be complete before a card is done.\nfunc CheckDependencies(task Task) error {\n\tfor _, p := range task.Prerequisites {\n\t\tif !p.Done {\n\t\t\treturn ErrUnmetPrerequisite\n\t\t}\n\t}\n\treturn nil\n}\n",
	"web/cards.ts":         "// Card prerequisite labels.\nexport function prerequisiteLabel(count: number): string {\n  return count === 1 ? '1 prerequisite' : `${count} prerequisites`;\n}\nconst sample = '<script>alert(1)</script>';\n",
	"internal/health.go":   "package app\n\n// Placeholder only; the route is not registered yet.\nfunc healthStatus() string { return \"unknown\" }\n",
	"internal/export.go":   "package app\n\n// Export writes CSV rows. JSON export is not implemented.\nfunc ExportCSV(rows []Row) []byte { return encodeCSV(rows) }\n",
	"internal/sync.go":     "package app\n\n// SyncBoard reconciles remote columns.\nfunc SyncBoard() error { return syncColumns() }\n",
	"internal/crash.go":    "package app\n\n// Guard nil task pointers before rendering.\nfunc Render(t *Task) string {\n\tif t == nil {\n\t\treturn \"\"\n\t}\n\treturn t.Title\n}\n",
	"internal/login.go":    "package app\n\n// Session refresh still drops the remember-me flag.\nfunc Refresh(s Session) Session { return Session{ID: s.ID} }\n",
	"internal/vendor.go":   "package app\n\n// Vendor integration pending approval.\n",
	"internal/archive.go":  "package app\n\n// Archive moves finished cards out of the board.\nfunc Archive(t *Task) { t.Archived = true }\n",
	"internal/filler.go":   "package app\n\n// Completed synthetic maintenance work.\nfunc Maintenance() bool { return true }\n",
	"docs/README.md":       "# Synthetic\nUnrelated documentation that the audit does not need to read.\n",
	"assets/unrelated.png": "unread",
}

type columnAuditUpstream struct {
	mu        sync.Mutex
	revoked   bool
	advanced  bool
	requests  int
	inference int
	delayMS   int
	aiOffline bool
}

func (u *columnAuditUpstream) baseline() string {
	if u.advanced {
		return columnAuditAdvanceSHA
	}
	return columnAuditBaseSHA
}

func (u *columnAuditUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.requests++
	revoked, baseline := u.revoked, u.baseline()
	u.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if revoked || r.Header.Get("Authorization") != "Bearer "+columnAuditToken {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Synthetic repository access revoked"}`)
		return
	}
	path := r.URL.Path
	switch {
	case path == "/search/code":
		fmt.Fprint(w, `{"total_count":0,"incomplete_results":false,"items":[]}`)
	case strings.Contains(path, "/git/trees/"):
		tree := []map[string]any{}
		for name, text := range columnAuditFiles {
			tree = append(tree, map[string]any{"path": name, "type": "blob", "mode": "100644", "sha": columnAuditBlobSHA, "size": len(text)})
		}
		json.NewEncoder(w).Encode(map[string]any{"tree": tree, "truncated": false})
	case strings.Contains(path, "/contents/"):
		text, ok := columnAuditFiles[strings.SplitN(path, "/contents/", 2)[1]]
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "file", "encoding": "base64", "sha": columnAuditBlobSHA, "size": len(text), "content": base64.StdEncoding.EncodeToString([]byte(text))})
	case strings.Contains(path, "/commits/") && !strings.HasSuffix(path, "/statuses") && !strings.HasSuffix(path, "/check-runs"):
		json.NewEncoder(w).Encode(map[string]any{"sha": baseline})
	case strings.HasSuffix(path, "/pulls/7/files"):
		json.NewEncoder(w).Encode([]map[string]any{{"filename": "internal/deps.go"}})
	case strings.HasSuffix(path, "/pulls/7"):
		json.NewEncoder(w).Encode(map[string]any{"title": "Synthetic dependency checks", "state": "open", "draft": false, "head": map[string]any{"sha": columnAuditPRHeadSHA}, "base": map[string]any{"sha": baseline, "repo": map[string]any{"id": 1}}})
	case strings.HasSuffix(path, "/check-runs"):
		fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
	case strings.HasSuffix(path, "/statuses"), strings.HasSuffix(path, "/reviews"), strings.HasSuffix(path, "/pulls"):
		fmt.Fprint(w, `[]`)
	default:
		fmt.Fprintf(w, `{"id":1,"full_name":%q,"default_branch":"main","permissions":{"pull":true}}`, columnAuditRepository)
	}
}

type columnAuditSeedTask struct {
	Tag, Title, Description, State, Kind string
}

// Lifecycle seeds realistic synthetic cards. Fillers precede the reopening
// card so it lands beyond the board's first Done page.
func columnAuditSeed(scenario string, fillers int) []columnAuditSeedTask {
	if scenario == "unverified" {
		return []columnAuditSeedTask{
			{"health", "Add health endpoint", "Scenario: unknown\nGoal: Serve readiness from internal/health.go.", "active", "task"},
			{"vendor", "Integrate vendor billing", "Scenario: blocked-unknown\nGoal: Integrate billing in internal/vendor.go.\nBlocker: Waiting for vendor approval.", "blocked", "task"},
		}
	}
	tasks := []columnAuditSeedTask{
		{"complete", "Add task dependency checks", "Scenario: complete\nAcceptance criteria\n- Reject completion with unmet prerequisites in internal/deps.go.\n- Show prerequisite labels on cards in web/cards.ts.", "active", "task"},
		{"correct", "Archive finished cards", "Scenario: correct\nGoal: Archive finished cards in internal/archive.go.", "completed", "task"},
		{"partial", "Sync board columns", "Scenario: partial-active\nAcceptance criteria\n- Reconcile remote columns in internal/sync.go.\n- Report conflicts in internal/export.go.", "active", "task"},
		{"unknown", "Add health endpoint", "Scenario: unknown\nGoal: Serve readiness from internal/health.go.", "active", "task"},
		{"blocked", "Integrate vendor billing", "Scenario: blocked-unknown\nGoal: Integrate billing in internal/vendor.go.\nBlocker: Waiting for vendor approval.", "blocked", "task"},
		{"bugfix", "Crash when a card has no task", "Scenario: bug-resolve\nGoal: Guard nil tasks in internal/crash.go.", "active", "bug"},
		{"bugreopen", "Remember-me lost on refresh", "Scenario: bug-reopen\nAcceptance criteria\n- Refresh sessions in internal/login.go.\n- Preserve the remember-me flag in internal/login.go.", "completed", "bug"},
		{"claimed", "Render dependency badges", "Scenario: claimed\nGoal: Show prerequisite labels on cards in web/cards.ts.", "active", "task"},
		{"stale", "Check prerequisites on completion", "Scenario: stale\nGoal: Reject completion with unmet prerequisites in internal/deps.go.", "active", "task"},
	}
	for i := 0; i < fillers; i++ {
		tasks = append(tasks, columnAuditSeedTask{fmt.Sprintf("filler-%d", i), fmt.Sprintf("Maintenance task %d", i+1), "Scenario: correct\nGoal: Maintain internal/filler.go.", "completed", "task"})
	}
	return append(tasks, columnAuditSeedTask{"reopen", "Export board as JSON", "Scenario: reopen\nAcceptance criteria\n- Export CSV rows in internal/export.go.\n- Export JSON documents in internal/export.go.", "completed", "task"})
}

func seedColumnAuditProject(ctx context.Context, d *store.Store, actor store.Actor, scenario string, fillers int, suffix string) (map[string]any, error) {
	key, name := "CA"+suffix, "Column audit "+suffix
	project, err := d.CreateProject(ctx, store.ProjectInput{Key: &key, Name: &name}, actor.ID)
	if err != nil {
		return nil, err
	}
	if _, err = d.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: columnAuditRepository, Ref: "main"}}, 1); err != nil {
		return nil, err
	}
	columns := map[string]string{}
	rows, err := d.DB.QueryContext(ctx, `SELECT id,semantic_state FROM columns WHERE project_id=? ORDER BY position`, project.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, state string
		if err = rows.Scan(&id, &state); err == nil && columns[state] == "" {
			columns[state] = id
		}
	}
	rows.Close()
	out := map[string]any{}
	for _, seed := range columnAuditSeed(scenario, fillers) {
		input := store.TaskInput{Title: stringPtr(seed.Title), Description: stringPtr(seed.Description)}
		if seed.Kind == "bug" {
			input.Kind = stringPtr("bug")
			input.Bug = &store.BugInput{ActualBehavior: stringPtr("Synthetic regression for " + seed.Title)}
		}
		task, err := d.CreateTask(ctx, project.ID, input, actor.ID)
		if err != nil {
			return nil, err
		}
		if seed.State != "backlog" {
			target := columns["active"]
			if seed.State == "blocked" {
				target = columns["blocked"]
			}
			if task, err = d.UpdateTask(ctx, task.ID, store.TaskInput{ColumnID: &target}, task.Version, actor.ID); err != nil {
				return nil, err
			}
		}
		if seed.State == "completed" && seed.Kind == "bug" {
			task, err = d.ResolveBug(ctx, task.ID, store.ResolveBugInput{Resolution: "fixed"}, task.Version, actor.ID)
		} else if seed.State == "completed" {
			task, err = d.CompleteTask(ctx, task.ID, actor.ID, task.Version)
		}
		if err != nil {
			return nil, err
		}
		if seed.Tag == "complete" {
			if _, task, err = d.LinkGithubArtifact(ctx, task.ID, columnAuditRepository, 7, "https://github.com/"+columnAuditRepository+"/pull/7", task.Version, actor.ID, true); err != nil {
				return nil, err
			}
		}
		if seed.Tag == "claimed" {
			agent, err := d.CreateActor(ctx, store.Actor{Kind: "agent", Name: "Synthetic agent " + suffix}, "")
			if err != nil {
				return nil, err
			}
			if _, err = d.DB.ExecContext(ctx, `UPDATE tasks SET claimed_by=?,claim_expires_at='2099-01-01T00:00:00Z' WHERE id=?`, agent.ID, task.ID); err != nil {
				return nil, err
			}
		}
		out[seed.Tag] = map[string]any{"id": task.ID, "key": task.Key, "title": task.Title}
	}
	return map[string]any{"project": map[string]any{"id": project.ID, "slug": project.Slug, "key": project.Key, "name": project.Name}, "tasks": out, "columns": columns}, nil
}
