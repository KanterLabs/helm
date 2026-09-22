package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/KanterLabs/helm/internal/db"
)

func TestLunaRunHistoryIsActorScopedAndBounded(t *testing.T) {
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
	project, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("LUNA"), Name: stringPtrForTest("Luna")}, first.ID)
	if err != nil {
		t.Fatal(err)
	}

	run, err := data.StartLunaRun(ctx, LunaRunStart{ActorID: first.ID, ProjectID: project.ID, ProjectKey: project.Key, Feature: "task_draft", Model: "gpt-5.6-luna", Effort: "medium"})
	if err != nil {
		t.Fatal(err)
	}
	if err := data.FinishLunaRun(ctx, run.ID, LunaRunFinish{Outcome: "invalid_output", ThreadID: "thread-1", TurnID: "turn-1", DurationMS: 42, OutputBytes: 123, Detail: "unsupported task key"}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxLunaRunsPerActor; index++ {
		created, err := data.StartLunaRun(ctx, LunaRunStart{ActorID: first.ID, Feature: "project_intelligence", Model: "gpt-5.6-luna", Effort: "low"})
		if err != nil {
			t.Fatalf("start %d: %v", index, err)
		}
		if err := data.FinishLunaRun(ctx, created.ID, LunaRunFinish{Outcome: "succeeded", ThreadID: fmt.Sprintf("thread-%d", index), DurationMS: 1}); err != nil {
			t.Fatalf("finish %d: %v", index, err)
		}
	}

	runs, err := data.ListLunaRuns(ctx, first.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 100 || runs[0].Outcome != "succeeded" || runs[0].Feature != "project_intelligence" {
		t.Fatalf("runs=%d first=%+v", len(runs), runs[0])
	}
	var retained int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM luna_runs WHERE actor_id=?`, first.ID).Scan(&retained); err != nil || retained != maxLunaRunsPerActor {
		t.Fatalf("retained=%d err=%v", retained, err)
	}
	other, err := data.ListLunaRuns(ctx, second.ID, 25)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-actor history=%+v err=%v", other, err)
	}
}
