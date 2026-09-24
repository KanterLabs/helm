package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/db"
)

func TestLunaRunStepsAreOrderedBoundedAndSafe(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Luna user"}, "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := data.StartLunaRun(ctx, LunaRunStart{ActorID: actor.ID, Feature: "task_draft", Model: "gpt-5.6-luna", Effort: "medium"})
	if err != nil {
		t.Fatal(err)
	}

	for _, input := range []LunaRunStepInput{
		{Kind: "thread_started"},
		{Kind: "turn_started"},
		{Kind: "response_generated"},
		{Kind: "validation"},
		{Kind: "outcome"},
	} {
		if err := data.AppendLunaRunStep(ctx, run.ID, input); err != nil {
			t.Fatalf("append %+v: %v", input, err)
		}
	}

	runs, err := data.ListLunaRuns(ctx, actor.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || len(runs[0].Steps) != 5 {
		t.Fatalf("runs=%+v", runs)
	}
	for index, step := range runs[0].Steps {
		if step.Sequence != index+1 || step.At == "" {
			t.Fatalf("step %d=%+v", index, step)
		}
		if step.Kind == "" {
			t.Fatalf("step %d missing safe fields: %+v", index, step)
		}
	}
	encoded, err := json.Marshal(runs[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || string(encoded) == "null" {
		t.Fatalf("empty run response: %s", encoded)
	}
	for _, forbidden := range []string{"prompt", "params", "secret prompt", "model output"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("unsafe step content exposed: %q in %s", forbidden, encoded)
		}
	}

	for index := 0; index < maxLunaRunSteps+4; index++ {
		if err := data.AppendLunaRunStep(ctx, run.ID, LunaRunStepInput{Kind: "outcome"}); err != nil {
			t.Fatalf("bounded append %d: %v", index, err)
		}
	}
	runs, err = data.ListLunaRuns(ctx, actor.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(runs[0].Steps); got != maxLunaRunSteps {
		t.Fatalf("step count=%d, want cap %d", got, maxLunaRunSteps)
	}
	for index, step := range runs[0].Steps {
		if step.Sequence != index+1 {
			t.Fatalf("step %d sequence=%d", index, step.Sequence)
		}
	}
}

func TestLunaRunStepsRejectUnknownCategoriesAndIgnoreFinishedRuns(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Luna user"}, "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := data.StartLunaRun(ctx, LunaRunStart{ActorID: actor.ID, Feature: "task_draft", Model: "gpt-5.6-luna", Effort: "medium"})
	if err != nil {
		t.Fatal(err)
	}
	if err := data.AppendLunaRunStep(ctx, run.ID, LunaRunStepInput{Kind: "raw_event"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind error=%v, want ErrInvalid", err)
	}
	if err := data.FinishLunaRun(ctx, run.ID, LunaRunFinish{Outcome: "succeeded", DurationMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := data.AppendLunaRunStep(ctx, run.ID, LunaRunStepInput{Kind: "outcome"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("finished run error=%v, want ErrConflict", err)
	}
	runs, err := data.ListLunaRuns(ctx, actor.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || len(runs[0].Steps) != 0 {
		t.Fatalf("finished run steps=%+v", runs)
	}
}
