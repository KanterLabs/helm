package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

func TestLunaRunsListsOnlyCurrentHumanHistoryWithoutCodexService(t *testing.T) {
	server, data := testServer(t, "disabled")
	actor, err := data.EnsureDisabledActor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	run, err := data.StartLunaRun(context.Background(), store.LunaRunStart{ActorID: actor.ID, Feature: "project_intelligence", Model: "gpt-5.6-luna", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if err := data.FinishLunaRun(context.Background(), run.ID, store.LunaRunFinish{Outcome: "invalid_output", ThreadID: "thread-debug", TurnID: "turn-debug", DurationMS: 18, OutputBytes: 44, Detail: "missing project result"}); err != nil {
		t.Fatal(err)
	}

	response := request(t, server, http.MethodGet, "/api/v1/codex/runs?limit=10", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Data []store.LunaRun `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 1 || payload.Data[0].ThreadID != "thread-debug" || payload.Data[0].Detail != "missing project result" {
		t.Fatalf("history=%+v", payload.Data)
	}
	if strings.Contains(response.Body.String(), actor.ID) {
		t.Fatalf("actor identifier leaked in response: %s", response.Body.String())
	}

	invalid := request(t, server, http.MethodGet, "/api/v1/codex/runs?limit=101", nil, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}
