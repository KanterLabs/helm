package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/db"
)

func TestLunaRunContentIsBoundedActorScopedAndAbsentFromList(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	data := New(database)
	first, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "First"}, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Second"}, "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := data.StartLunaRun(ctx, LunaRunStart{ActorID: first.ID, Feature: "task_draft", Model: "gpt-5.6-luna", Effort: "medium"})
	if err != nil {
		t.Fatal(err)
	}
	input := "private prompt " + strings.Repeat("i", maxLunaRunInputBytes)
	output := "partial invalid model output " + strings.Repeat("o", maxLunaRunOutputBytes)
	if err := data.SaveLunaRunInput(ctx, run.ID, input); err != nil {
		t.Fatal(err)
	}
	if err := data.SaveLunaRunOutput(ctx, run.ID, output, true); err != nil {
		t.Fatal(err)
	}
	got, content, available, err := data.GetLunaRunDetail(ctx, first.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != run.ID || !available {
		t.Fatalf("detail run=%+v available=%v", got, available)
	}
	if len(content.InputText) > maxLunaRunInputBytes || !content.InputTruncated || len(content.OutputText) > maxLunaRunOutputBytes || !content.OutputTruncated {
		t.Fatalf("content bounds=%+v", content)
	}
	if !strings.HasPrefix(content.InputText, "private prompt ") {
		t.Fatalf("input prefix lost: %q", content.InputText[:minLunaContentPreview(len(content.InputText), 32)])
	}
	if !strings.HasPrefix(content.OutputText, "partial invalid model output") {
		t.Fatalf("output prefix lost: %q", content.OutputText[:minLunaContentPreview(len(content.OutputText), 32)])
	}
	if _, _, _, err := data.GetLunaRunDetail(ctx, second.ID, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-actor detail error=%v, want ErrNotFound", err)
	}
	runs, err := data.ListLunaRuns(ctx, first.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(runs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private prompt") || strings.Contains(string(encoded), "partial invalid") {
		t.Fatalf("metadata list exposed captured content: %s", encoded)
	}

	legacy, err := data.StartLunaRun(ctx, LunaRunStart{ActorID: first.ID, Feature: "project_intelligence", Model: "gpt-5.6-luna", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if err := data.FinishLunaRun(ctx, legacy.ID, LunaRunFinish{Outcome: "succeeded", DurationMS: 1}); err != nil {
		t.Fatal(err)
	}
	_, _, available, err = data.GetLunaRunDetail(ctx, first.ID, legacy.ID)
	if err != nil || available {
		t.Fatalf("legacy detail available=%v err=%v, want unavailable", available, err)
	}
}

func minLunaContentPreview(value, limit int) int {
	if value < limit {
		return value
	}
	return limit
}
