package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

// Failure inventory: docs/AUDIT_CORRECTION_FAILURES.md. Written before code.
func TestAuditCorrectionHTTPLifecycle(t *testing.T) {
	for _, action := range []string{"complete", "reopen", "bug_resolve", "bug_reopen"} {
		t.Run(action, func(t *testing.T) {
			f := newCorrectionFixture(t, action)
			inputs := map[string]any{}
			if action == "bug_resolve" {
				inputs["resolution"] = "fixed"
			}
			if action == "bug_reopen" {
				inputs["reason"] = "Synthetic regression remains reproducible"
			}
			before := f.previewSnapshot(t)
			code, raw := f.post(t, "/apply", f.request(true, "", inputs), "")
			row := correctionRow(t, code, raw)
			if row["outcome"] != "eligible" {
				t.Fatalf("preview %s", raw)
			}
			token, _ := row["confirmation_token"].(string)
			if token == "" {
				t.Fatal("no confirmation proof")
			}
			afterPreview := f.previewSnapshot(t)
			if afterPreview != before {
				t.Fatal("preview changed database")
			}
			code, raw = f.post(t, "/apply", f.request(false, token, inputs), "apply-once")
			row = correctionRow(t, code, raw)
			if row["outcome"] != "applied" {
				t.Fatalf("apply %s", raw)
			}
			task, err := f.data.GetTask(context.Background(), f.task.ID)
			if err != nil {
				t.Fatal(err)
			}
			done := action == "complete" || action == "bug_resolve"
			if (task.CompletedAt != nil) != done {
				t.Fatalf("completion timestamp %+v", task)
			}
			if task.Kind == "bug" && (task.Bug.Resolution != nil) != done {
				t.Fatal("bug resolution lifecycle lost")
			}
			applied := f.snapshot(t)
			// A durable result wins over stale remote/local facts when replaying this key.
			_, err = f.data.UpdateGithubSelection(context.Background(), f.project.ID, []store.GithubRepository{}, 2)
			if err != nil {
				t.Fatal(err)
			}
			code, retry := f.post(t, "/apply", f.request(false, token, inputs), "apply-once")
			again := correctionRow(t, code, retry)
			if again["outcome"] != "already_applied" {
				t.Fatalf("retry %s", retry)
			}
			if f.snapshot(t) != applied {
				t.Fatal("retry changed lifecycle/history")
			}
			correctionArtifact(t, action, map[string]any{"apply": json.RawMessage(raw), "retry": json.RawMessage(retry), "task": task, "preview_read_only": true, "preview_before_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(before))), "preview_after_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(afterPreview)))})
		})
	}
}
func TestAuditCorrectionHTTPGuards(t *testing.T) {
	cases := []string{"task", "criteria", "target", "source", "mapping", "claim", "policy", "dependency", "selection", "forged", "actor", "missing-proof", "missing-input", "agent", "expired", "dismissed", "checklist"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			action := "complete"
			if name == "missing-input" {
				action = "bug_resolve"
			}
			if name == "dependency" {
				action = "complete-dependency"
			}
			f := newCorrectionFixture(t, action)
			code, raw := f.post(t, "/apply", f.request(true, "", nil), "")
			row := correctionRow(t, code, raw)
			token, _ := row["confirmation_token"].(string)
			if name == "missing-input" {
				if row["outcome"] != "skipped" {
					t.Fatalf("missing required bug input %s", raw)
				}
				return
			}
			if row["outcome"] != "eligible" {
				t.Fatalf("preview %s", raw)
			}
			var err error
			switch name {
			case "task":
				_, err = f.data.DB.Exec(`UPDATE tasks SET version=version+1 WHERE id=?`, f.task.ID)
			case "criteria":
				_, err = f.data.DB.Exec(`UPDATE tasks SET description='Goal: Changed criterion' WHERE id=?`, f.task.ID)
			case "target":
				_, err = f.data.DB.Exec(`UPDATE columns SET version=version+1 WHERE id=?`, *f.finding.Assessment.TargetColumnID)
			case "source":
				_, err = f.data.DB.Exec(`UPDATE columns SET version=version+1 WHERE id=?`, f.task.ColumnID)
			case "mapping":
				_, err = f.data.DB.Exec(`INSERT INTO columns(id,project_id,name,semantic_state,position,created_at,updated_at) VALUES('duplicate-done',?,'Other Done','completed',99,'synthetic','synthetic')`, f.project.ID)
			case "claim":
				other, e := f.data.CreateActor(context.Background(), store.Actor{Kind: "agent", Name: "Foreign synthetic agent"}, "")
				if e != nil {
					t.Fatal(e)
				}
				_, err = f.data.DB.Exec(`UPDATE tasks SET claimed_by=?,claim_expires_at='2099-01-01T00:00:00Z' WHERE id=?`, other.ID, f.task.ID)
			case "policy":
				_, err = f.data.DB.Exec(`UPDATE projects SET checklist_completion_policy='require' WHERE id=?`, f.project.ID)
			case "dependency":
				_, err = f.data.DB.Exec(`UPDATE tasks SET version=version+1 WHERE id=(SELECT prerequisite_task_id FROM task_dependencies WHERE task_id=?)`, f.task.ID)
			case "selection":
				_, err = f.data.UpdateGithubSelection(context.Background(), f.project.ID, []store.GithubRepository{}, 2)
			case "expired":
				token = signCorrectionProof(f.actor.ID, f.job.AuditID, f.finding.ID, row["fingerprint"].(string), 1)
			case "dismissed":
				_, err = f.data.DB.Exec(`UPDATE audit_findings SET review_state='dismissed',version=version+1 WHERE id=?`, f.finding.ID)
			case "checklist":
				_, err = f.data.DB.Exec(`INSERT INTO task_checklist_items(id,task_id,text,completed,position,created_at,updated_at) VALUES('new-item',?,'New condition',0,0,'now','now')`, f.task.ID)
			case "forged":
				token += "tampered"
			case "actor":
				a, e := f.data.CreateActor(context.Background(), store.Actor{Kind: "human", Name: "Second reviewer"}, "")
				err = e
				f.identity = auth.Identity{Actor: a}
			case "missing-proof":
				token = ""
			case "agent":
				f.identity.IsToken = true
			}
			if err != nil {
				t.Fatal(err)
			}
			before := f.snapshot(t)
			code, raw = f.post(t, "/apply", f.request(false, token, nil), "guard-"+name)
			if name == "agent" {
				if code != 403 {
					t.Fatalf("agent HTTP %d %s", code, raw)
				}
				return
			}
			row = correctionRow(t, code, raw)
			if row["outcome"] != "skipped" {
				t.Fatalf("guard %s", raw)
			}
			if f.snapshot(t) != before {
				t.Fatal("rejected apply wrote approval/lifecycle")
			}
			if name == "task" {
				correctionArtifact(t, "stale-skip", json.RawMessage(raw))
			}
		})
	}
}
func TestAuditCorrectionHTTPConcurrentRetry(t *testing.T) {
	f := newCorrectionFixture(t, "complete")
	code, raw := f.post(t, "/apply", f.request(true, "", nil), "")
	row := correctionRow(t, code, raw)
	token, _ := row["confirmation_token"].(string)
	if token == "" {
		t.Fatalf("preview %s", raw)
	}
	var wg sync.WaitGroup
	receipts := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, b := f.post(t, "/apply", f.request(false, token, nil), "concurrent")
			r := correctionRow(t, c, b)
			receipts <- r["outcome"].(string)
		}()
	}
	wg.Wait()
	close(receipts)
	outcomes := []string{}
	for s := range receipts {
		outcomes = append(outcomes, s)
	}
	joined := strings.Join(outcomes, ",")
	if !strings.Contains(joined, "already_applied") || !strings.Contains(joined, "applied") {
		t.Fatal(outcomes)
	}
	task, _ := f.data.GetTask(context.Background(), f.task.ID)
	if task.Version != f.task.Version+1 {
		t.Fatal("duplicate transition")
	}
	correctionArtifact(t, "concurrent", outcomes)
}
