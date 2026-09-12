package store

import (
	"context"
	"errors"
	"testing"

	"github.com/KanterLabs/helm/internal/db"
)

func TestReleaseLifecycleAndTaskBinding(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	owner, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Release owner"}, "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("REL"), Name: stringPtrForTest("Release project")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("OTHER"), Name: stringPtrForTest("Other project")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	date := "2026-11-15"
	description := "Search and release planning"
	release, err := data.CreateRelease(ctx, project.ID, ReleaseInput{Name: stringPtrForTest("1.4"), Description: &description, TargetDate: &date}, owner.ID)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	if release.Status != "planned" || release.Summary.TaskCount != 0 || release.Summary.ReadyToRelease {
		t.Fatalf("new release = %+v", release)
	}
	if _, err := data.CreateRelease(ctx, project.ID, ReleaseInput{Name: stringPtrForTest(" 1.4 ")}, owner.ID); !errors.Is(err, ErrReleaseNameExists) {
		t.Fatalf("case-insensitive duplicate error = %v, want ErrReleaseNameExists", err)
	}
	for _, reservedName := range []string{"unassigned", "NONE", " Unassigned "} {
		if _, err := data.CreateRelease(ctx, project.ID, ReleaseInput{Name: stringPtrForTest(reservedName)}, owner.ID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("reserved release name %q error = %v, want ErrInvalid", reservedName, err)
		}
	}
	if _, err := data.CreateRelease(ctx, project.ID, ReleaseInput{Name: stringPtrForTest("bad date"), TargetDate: stringPtrForTest("2026-02-30")}, owner.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid date error = %v, want ErrInvalid", err)
	}
	mutableName := "mutable"
	mutable, err := data.CreateRelease(ctx, project.ID, ReleaseInput{Name: &mutableName}, owner.ID)
	if err != nil {
		t.Fatalf("create mutable release: %v", err)
	}
	reservedUpdate := " none "
	if _, err := data.UpdateRelease(ctx, mutable.ID, ReleaseInput{Name: &reservedUpdate}, mutable.Version, owner.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reserved release rename error = %v, want ErrInvalid", err)
	}

	assigned, err := data.CreateTask(ctx, project.ID, TaskInput{Title: stringPtrForTest("Ship release"), ReleaseID: &release.ID}, owner.ID)
	if err != nil {
		t.Fatalf("create assigned task: %v", err)
	}
	if assigned.ReleaseID == nil || *assigned.ReleaseID != release.ID || assigned.Release == nil || assigned.Release.Name != release.Name || assigned.Release.Status != "planned" {
		t.Fatalf("assigned task release = %+v", assigned)
	}
	page, more, err := data.ListTasks(ctx, project.ID, TaskFilter{ReleaseID: release.ID, Limit: 10})
	if err != nil || more || len(page) != 1 || page[0].Release == nil {
		t.Fatalf("release-filtered tasks = %#v more=%v err=%v", page, more, err)
	}
	unassigned, _, err := data.ListTasks(ctx, project.ID, TaskFilter{ReleaseID: "unassigned", Limit: 10})
	if err != nil || len(unassigned) != 0 {
		t.Fatalf("unassigned tasks = %#v err=%v", unassigned, err)
	}

	prerequisite, err := data.CreateTask(ctx, project.ID, TaskInput{Title: stringPtrForTest("Prepare dependency")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	assigned, err = data.AddTaskDependency(ctx, assigned.ID, prerequisite.ID, assigned.Version, owner.ID)
	if err != nil {
		t.Fatalf("add prerequisite: %v", err)
	}
	release, err = data.GetRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if release.Summary.TaskCount != 1 || release.Summary.RequiredTaskCount != 2 || release.Summary.ReadyToRelease {
		t.Fatalf("incomplete release summary = %+v", release.Summary)
	}
	if _, err := data.CompleteRelease(ctx, release.ID, release.Version, owner.ID); !errors.Is(err, ErrReleaseIncomplete) {
		t.Fatalf("incomplete release completion error = %v, want ErrReleaseIncomplete", err)
	}

	prerequisite, err = data.CompleteTask(ctx, prerequisite.ID, owner.ID, prerequisite.Version)
	if err != nil {
		t.Fatalf("complete prerequisite: %v", err)
	}
	assigned, err = data.CompleteTask(ctx, assigned.ID, owner.ID, assigned.Version)
	if err != nil {
		t.Fatalf("complete assigned task: %v", err)
	}
	release, err = data.GetRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !release.Summary.ReadyToRelease || release.Summary.RequiredCompletedCount != 2 {
		t.Fatalf("ready release summary = %+v", release.Summary)
	}
	released, err := data.CompleteRelease(ctx, release.ID, release.Version, owner.ID)
	if err != nil {
		t.Fatalf("complete release: %v", err)
	}
	if released.Status != "released" || released.ReleasedAt == nil || released.ReleasedBy == nil || *released.ReleasedBy != owner.ID {
		t.Fatalf("released release = %+v", released)
	}
	if _, err := data.SetTaskRelease(ctx, assigned.ID, nil, assigned.Version, owner.ID); !errors.Is(err, ErrReleaseFrozen) {
		t.Fatalf("released membership mutation error = %v, want ErrReleaseFrozen", err)
	}
	if err := data.DeleteRelease(ctx, released.ID, released.Version, owner.ID); !errors.Is(err, ErrReleaseFrozen) {
		t.Fatalf("released delete error = %v, want ErrReleaseFrozen", err)
	}
	if _, err := data.ReopenRelease(ctx, released.ID, "", released.Version, owner.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty reopen reason error = %v, want ErrInvalid", err)
	}
	reopened, err := data.ReopenRelease(ctx, released.ID, "Scope changed", released.Version, owner.ID)
	if err != nil || reopened.Status != "planned" || reopened.Version != released.Version+1 {
		t.Fatalf("reopened release = %+v err=%v", reopened, err)
	}
	assigned, err = data.SetTaskRelease(ctx, assigned.ID, nil, assigned.Version, owner.ID)
	if err != nil || assigned.ReleaseID != nil || assigned.Release != nil {
		t.Fatalf("cleared task release = %+v err=%v", assigned, err)
	}
	if err := data.DeleteRelease(ctx, reopened.ID, reopened.Version, owner.ID); err != nil {
		t.Fatalf("delete empty planned release: %v", err)
	}

	foreign, err := data.CreateRelease(ctx, otherProject.ID, ReleaseInput{Name: stringPtrForTest("1.4")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := data.CreateTask(ctx, project.ID, TaskInput{Title: stringPtrForTest("Foreign assignment target")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.AssignTaskRelease(ctx, ordinary.ID, foreign.ID, ordinary.Version, owner.ID); !errors.Is(err, ErrReleaseCrossProject) {
		t.Fatalf("cross-project assignment error = %v, want ErrReleaseCrossProject", err)
	}
}

func TestReleaseSummaryFlagsIncompletePrerequisiteInAnotherPlannedRelease(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	owner, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Release owner"}, "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("PLAN"), Name: stringPtrForTest("Planning conflicts")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := data.CreateRelease(ctx, project.ID, ReleaseInput{Name: stringPtrForTest("1.0")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := data.CreateRelease(ctx, project.ID, ReleaseInput{Name: stringPtrForTest("2.0")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	dependent, err := data.CreateTask(ctx, project.ID, TaskInput{Title: stringPtrForTest("Ship 1.0"), ReleaseID: &first.ID}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	prerequisite, err := data.CreateTask(ctx, project.ID, TaskInput{Title: stringPtrForTest("Work planned for 2.0"), ReleaseID: &second.ID}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddTaskDependency(ctx, dependent.ID, prerequisite.ID, dependent.Version, owner.ID); err != nil {
		t.Fatalf("add cross-release dependency: %v", err)
	}
	first, err = data.GetRelease(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Summary.CrossReleaseConflictCount != 1 || first.Summary.ReadyToRelease {
		t.Fatalf("cross-release summary = %+v", first.Summary)
	}
	if _, err := data.CompleteRelease(ctx, first.ID, first.Version, owner.ID); !errors.Is(err, ErrReleaseDependencyConflict) {
		t.Fatalf("cross-release completion error = %v, want ErrReleaseDependencyConflict", err)
	}
}
