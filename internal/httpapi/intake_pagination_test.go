package httpapi

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

func TestIntakeCursorMutationHTTP(t *testing.T) {
	j, data := newIntakeJourney(t)
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("MUT"), Name: stringPtr("Synthetic cursor mutation")}, actor.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		j.call("create-"+string(rune('a'+i)), "POST", "/api/v1/projects/MUT/tickets", map[string]any{"title": "Synthetic mutation request"}, "", 201)
	}
	first := j.list("first", "?limit=1", "")
	if len(first.Data) != 1 || first.Next == "" {
		t.Fatalf("first: %+v", first)
	}
	j.call("complete-first", "POST", "/api/v1/tasks/"+first.Data[0].ID+"/complete", map[string]any{}, "", 200)
	j.call("must-restart", "GET", "/api/v1/intake?limit=1&cursor="+url.QueryEscape(first.Next), nil, "", 409)
}

func TestIntakeCursorSnapshotAndFilterHTTP(t *testing.T) {
	j, data := newIntakeJourney(t)
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("PAG"), Name: stringPtr("Synthetic pagination")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		j.call("create-"+string(rune('a'+i)), "POST", "/api/v1/projects/PAG/tickets", map[string]any{"title": "Synthetic pagination request"}, "", 201)
	}
	first := j.list("first", "?limit=1", "")
	if len(first.Data) != 1 || first.Next == "" {
		t.Fatalf("first page: %+v", first)
	}
	cursor := url.QueryEscape(first.Next)
	// A cursor must retain the original query and effective reader authority.
	j.call("scope-mismatch", "GET", "/api/v1/intake?scope=all&limit=1&cursor="+cursor, nil, "", 400)
	j.call("query-mismatch", "GET", "/api/v1/intake?q=unmatched&limit=1&cursor="+cursor, nil, "", 400)
	agent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Synthetic cursor reader"}, actor.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := data.CreateTokenBy(ctx, agent.ID, actor.ID, "cursor-reader", []string{"tasks:read"}, []string{project.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	j.call("authority-mismatch", "GET", "/api/v1/intake?limit=1&cursor="+cursor, nil, token, 400)
	// Remove the already-read row from Needs triage. An offset would now skip
	// the next still-visible task; a stale snapshot must demand a clean restart.
	j.call("complete-first", "POST", "/api/v1/tasks/"+first.Data[0].ID+"/complete", map[string]any{}, "", 200)
	raw := j.call("membership-changed", "GET", "/api/v1/intake?limit=1&cursor="+cursor, nil, "", 409)
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Restart bool `json:"restart"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "task_collection_changed" || !envelope.Error.Details.Restart {
		t.Fatalf("missing restart contract: %s", raw)
	}
	restarted := j.list("restarted", "", "")
	if len(restarted.Data) != 2 || restarted.Counts.Triage != 2 || restarted.Counts.All != 3 {
		t.Fatalf("restart lost a visible row: %+v", restarted)
	}
}
