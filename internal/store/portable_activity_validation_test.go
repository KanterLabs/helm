package store

import (
	"context"
	"errors"
	"github.com/KanterLabs/helm/internal/db"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableAgentWorkHistoryConstraintsArePreflightedWithoutMutation(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-history-constraint.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("HISTORY"), Name: portableTestString("History")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := data.ListColumns(ctx, project.ID)
	if err != nil || len(columns) == 0 {
		t.Fatalf("columns = %d, err=%v", len(columns), err)
	}
	timestamp := updatedTimestampForPortableTest()
	completed, total := 2, 1
	archive := PortableArchive{
		Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
		Projects: []PortableProject{{ID: "history-source-project", Key: "HISTSOURCE", Slug: "history-source", Name: "History source", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
		Columns:  []PortableColumn{{ID: "history-source-column", ProjectID: "history-source-project", Name: "Backlog", SemanticState: "backlog", Position: 0, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Tasks:    []PortableTask{{ID: "history-source-task", Number: 1, ProjectID: "history-source-project", Kind: "task", ColumnID: "history-source-column", Title: "History task", Priority: "normal", Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Labels:   []PortableLabel{}, Comments: []PortableComment{},
		Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
		Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{{ID: "history-invalid", TaskID: "history-source-task", OperationID: "history-operation", ActorID: actor.ID, State: "working", Phase: "test", Summary: "Invalid checkpoint", CheckpointRefs: []string{"checkpoint"}, CheckpointCompleted: &completed, CheckpointTotal: &total, StartedAt: timestamp, CreatedAt: timestamp}}},
	}
	var projects, tasks, histories int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_agent_work_history`).Scan(&histories); err != nil {
		t.Fatal(err)
	}
	preview, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, DryRun: true})
	if !errors.Is(err, ErrInvalid) || !portableReportHasIssue(preview, "agent_work_history", "checkpoint", "checkpoint progress must satisfy 0 <= completed <= total <= 100") {
		t.Fatalf("history constraint dry-run error=%v report=%+v", err, preview)
	}
	result, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID})
	if !errors.Is(err, ErrInvalid) || !portableReportHasIssue(result, "agent_work_history", "checkpoint", "checkpoint progress must satisfy 0 <= completed <= total <= 100") {
		t.Fatalf("history constraint import error=%v report=%+v", err, result)
	}
	var afterProjects, afterTasks, afterHistories int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&afterProjects); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&afterTasks); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_agent_work_history`).Scan(&afterHistories); err != nil {
		t.Fatal(err)
	}
	if projects != afterProjects || tasks != afterTasks || histories != afterHistories {
		t.Fatalf("invalid history import mutated counts before=%d/%d/%d after=%d/%d/%d", projects, tasks, histories, afterProjects, afterTasks, afterHistories)
	}
}

func TestPortableAgentWorkValidationMatchesRuntimeConstraints(t *testing.T) {
	type mutation struct {
		name    string
		apply   func(*AgentWorkInput)
		field   string
		message string
	}
	mutations := []mutation{
		{name: "operation id regex", apply: func(input *AgentWorkInput) { input.OperationID = "invalid operation" }, field: "operation_id", message: "operation_id must be between 1 and 128 safe identifier characters"},
		{name: "phase length", apply: func(input *AgentWorkInput) { input.Phase = strings.Repeat("p", 121) }, field: "phase", message: "phase is too long"},
		{name: "next action length", apply: func(input *AgentWorkInput) { input.NextAction = strings.Repeat("n", 1001) }, field: "next_action", message: "next_action is too long"},
		{name: "empty checkpoint reference", apply: func(input *AgentWorkInput) { input.CheckpointRefs = []string{""} }, field: "checkpoint_refs", message: "checkpoint_refs items must be between 1 and 128 characters"},
		{name: "duplicate checkpoint reference", apply: func(input *AgentWorkInput) {
			input.CheckpointRefs, input.CheckpointCompleted, input.CheckpointTotal = []string{"checkpoint", "checkpoint"}, portableTestIntPointer(0), portableTestIntPointer(2)
		}, field: "checkpoint_refs", message: "checkpoint_refs must not contain duplicate items"},
		{name: "checkpoint reference count", apply: func(input *AgentWorkInput) { input.CheckpointRefs = make([]string, 101) }, field: "checkpoint_refs", message: "checkpoint_refs must contain at most 100 items"},
		{name: "checkpoint reference length", apply: func(input *AgentWorkInput) { input.CheckpointRefs = []string{strings.Repeat("r", 129)} }, field: "checkpoint_refs", message: "checkpoint_refs items must be between 1 and 128 characters"},
		{name: "checkpoint total consistency", apply: func(input *AgentWorkInput) {
			input.CheckpointCompleted, input.CheckpointTotal = portableTestIntPointer(0), portableTestIntPointer(2)
		}, field: "checkpoint_refs", message: "checkpoint_refs length must equal checkpoint_total"},
		{name: "checkpoint pair consistency", apply: func(input *AgentWorkInput) { input.CheckpointTotal = nil }, field: "checkpoint", message: "checkpoint_completed and checkpoint_total must be provided together"},
		{name: "state", apply: func(input *AgentWorkInput) { input.State = "paused" }, field: "state", message: "state must be working, waiting, verifying, or handoff"},
	}
	for _, recordKind := range []string{"agent work", "history"} {
		for _, testCase := range mutations {
			t.Run(recordKind+"/"+testCase.name, func(t *testing.T) {
				ctx := context.Background()
				database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-agent-work-validation.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer database.Close()
				data := New(database)
				actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
				if err != nil {
					t.Fatal(err)
				}
				timestamp := updatedTimestampForPortableTest()
				completed, total := 0, 1
				input := AgentWorkInput{OperationID: "portable/activity", State: "working", Phase: "test", Summary: "Summary", NextAction: "Next", CheckpointRefs: []string{"checkpoint"}, CheckpointCompleted: &completed, CheckpointTotal: &total}
				testCase.apply(&input)
				archive := PortableArchive{
					Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
					Projects: []PortableProject{{ID: "activity-project", Key: "ACTIVITY", Slug: "activity", Name: "Activity", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
					Columns:  []PortableColumn{{ID: "activity-column", ProjectID: "activity-project", Name: "Backlog", SemanticState: "backlog", Position: 0, CreatedAt: timestamp, UpdatedAt: timestamp}},
					Tasks:    []PortableTask{{ID: "activity-task", Number: 1, ProjectID: "activity-project", Kind: "task", ColumnID: "activity-column", Title: "Activity task", Priority: "normal", Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
					Labels:   []PortableLabel{}, Comments: []PortableComment{},
					Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
					Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
				}
				if recordKind == "agent work" {
					archive.Activity.AgentWork = []PortableAgentWork{{TaskID: inputTaskID, OperationID: input.OperationID, ActorID: actor.ID, State: input.State, Phase: input.Phase, Summary: input.Summary, NextAction: input.NextAction, CheckpointRefs: input.CheckpointRefs, CheckpointCompleted: input.CheckpointCompleted, CheckpointTotal: input.CheckpointTotal, StartedAt: timestamp, UpdatedAt: timestamp}}
				} else {
					archive.Activity.AgentWorkHistory = []PortableAgentWorkHistory{{ID: "activity-history", TaskID: inputTaskID, OperationID: input.OperationID, ActorID: actor.ID, State: input.State, Phase: input.Phase, Summary: input.Summary, NextAction: input.NextAction, CheckpointRefs: input.CheckpointRefs, CheckpointCompleted: input.CheckpointCompleted, CheckpointTotal: input.CheckpointTotal, StartedAt: timestamp, CreatedAt: timestamp}}
				}
				counts := func() (int, int, int, int) {
					var projects, tasks, work, history int
					if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil {
						t.Fatal(err)
					}
					if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&tasks); err != nil {
						t.Fatal(err)
					}
					if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_agent_work`).Scan(&work); err != nil {
						t.Fatal(err)
					}
					if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_agent_work_history`).Scan(&history); err != nil {
						t.Fatal(err)
					}
					return projects, tasks, work, history
				}
				beforeProjects, beforeTasks, beforeWork, beforeHistory := counts()
				entity := "agent_work"
				if recordKind == "history" {
					entity = "agent_work_history"
				}
				preview, previewErr := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, DryRun: true})
				if !errors.Is(previewErr, ErrInvalid) || !portableReportHasIssue(preview, entity, testCase.field, testCase.message) {
					t.Fatalf("validation dry-run kind=%s error=%v report=%+v", recordKind, previewErr, preview)
				}
				result, resultErr := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID})
				if !errors.Is(resultErr, ErrInvalid) || !portableReportHasIssue(result, entity, testCase.field, testCase.message) {
					t.Fatalf("validation import kind=%s error=%v report=%+v", recordKind, resultErr, result)
				}
				afterProjects, afterTasks, afterWork, afterHistory := counts()
				if beforeProjects != afterProjects || beforeTasks != afterTasks || beforeWork != afterWork || beforeHistory != afterHistory {
					t.Fatalf("invalid %s import mutated counts before=%d/%d/%d/%d after=%d/%d/%d/%d", recordKind, beforeProjects, beforeTasks, beforeWork, beforeHistory, afterProjects, afterTasks, afterWork, afterHistory)
				}
			})
		}
	}
}

const inputTaskID = "activity-task"

func portableTestIntPointer(value int) *int { return &value }
