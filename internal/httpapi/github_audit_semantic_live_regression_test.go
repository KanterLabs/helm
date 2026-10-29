package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// Failure inventory from actual TC-311 inference: semantic discovery finds the
// owner, then repeats its package/import header. The refinement request must
// explicitly distinguish body inspection from the initial ownership read while
// retaining the quoted criterion, pinned source and server-extracted signatures.
func TestGithubAssessmentSemanticWorkerRequestsImplementationRefinement(t *testing.T) {
	_, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("BODYGAP"), Name: stringPtr("Synthetic body discovery")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Readiness"), Description: stringPtr("Goal: Let operators check that the database can accept work.")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	source := "package app\n"
	for i := 0; i < 119; i++ {
		source += fmt.Sprintf("func unrelated%d() {} // unrelated operator adapter padding owner\n", i)
	}
	source += "func ready(db Database) bool { return db.Ping() == nil }\n"
	f := &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": source}}
	ai := &assessmentAI{planOutputs: []string{
		`{"paths":[{"path":"internal/opaque.go","start_line":1,"end_line":1,"reason":"Find readiness owner"}],"gap":"Locate readiness"}`,
		`{"paths":[{"path":"internal/opaque.go","start_line":121,"end_line":121,"reason":"Read the indexed readiness function"}],"gap":"Inspect implementation beyond package header"}`,
	}}
	service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
	run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "body-refinement", Fingerprint: "body-refinement"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	discovery := []map[string]json.RawMessage{}
	for _, raw := range ai.receipts {
		var p map[string]json.RawMessage
		if err = json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		if string(p["phase"]) == `"discovery"` {
			discovery = append(discovery, p)
		}
	}
	if len(discovery) != 2 {
		t.Fatalf("expected bounded initial/refinement requests, got %d", len(discovery))
	}
	evidence, err := data.GetGithubAuditEvidence(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assessmentReceipt(t, "live-semantic-body", map[string]any{"evidence": evidence, "planning_calls": len(discovery)})
	var inspected []struct {
		Symbols []map[string]any `json:"symbols"`
	}
	if err = json.Unmarshal(discovery[1]["inspected"], &inspected); err != nil || len(inspected) != 1 {
		t.Fatal("missing structural symbol index")
	}
	symbols, _ := json.Marshal(inspected[0].Symbols)
	if !strings.Contains(string(symbols), "func unrelated32") {
		t.Fatal("whole-file sampling hides a middle-of-file behavior owner")
	}
	if !strings.Contains(string(discovery[1]["inspected"]), "func ready") {
		t.Fatal("refinement lost the server-indexed implementation signature")
	}
	if !strings.Contains(string(discovery[0]["instructions"]), "at most two production") {
		t.Fatal("initial semantic ownership must reserve the existing budget for body refinement")
	}
	if !strings.Contains(string(discovery[0]["instructions"]), "at most 101 lines") {
		t.Fatal("planner schema cannot enforce relative line bounds; instructions must state the server limit")
	}
	if string(discovery[0]["refinement"]) != "false" || string(discovery[1]["refinement"]) != "true" {
		t.Fatal("model cannot distinguish body refinement from initial header ownership read")
	}
}
