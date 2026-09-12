package store

import (
	"errors"
	"sync"
	"testing"
)

func TestReleaseWorkQueueScopesPrerequisitesAndInvalidatesCursor(t *testing.T) {
	f := newDependencyFixture(t, "RELQUEUE")
	releaseName := "1.4"
	release, err := f.store.CreateRelease(f.ctx, f.project.ID, ReleaseInput{Name: &releaseName}, f.actor.ID)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	prerequisite := f.task(t, "unassigned prerequisite")
	releaseID := release.ID
	direct, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: dependencyStringPtr("direct release member"), ReleaseID: &releaseID}, f.actor.ID)
	if err != nil {
		t.Fatalf("create direct release task: %v", err)
	}
	dependencyDirect, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: dependencyStringPtr("dependency-free direct member"), ReleaseID: &releaseID}, f.actor.ID)
	if err != nil {
		t.Fatalf("create dependency-free direct task: %v", err)
	}
	if _, err := f.store.AddTaskDependency(f.ctx, direct.ID, prerequisite.ID, direct.Version, f.actor.ID); err != nil {
		t.Fatalf("add release dependency: %v", err)
	}

	queue, err := f.store.GetReleaseWorkQueue(f.ctx, release.ID, f.actor.ID, ReleaseWorkQueueFilter{Limit: 20})
	if err != nil {
		t.Fatalf("read release queue: %v", err)
	}
	if queue.Summary.Direct != 2 || queue.Summary.Required != 3 || queue.Summary.DependencyBlocked != 1 || queue.Summary.Claimable != 2 {
		t.Fatalf("initial queue summary = %+v", queue.Summary)
	}
	var sawDirect, sawPrerequisite bool
	for _, item := range queue.Data {
		switch item.Task.ID {
		case direct.ID:
			sawDirect = item.Relationship == "direct" && item.Disposition == "dependency_blocked" && len(item.BlockedBy) == 1 && item.BlockedBy[0].ID == prerequisite.ID
		case prerequisite.ID:
			sawPrerequisite = item.Relationship == "prerequisite" && item.Disposition == "claimable"
		case dependencyDirect.ID:
			if item.Relationship != "direct" || item.Disposition != "claimable" {
				t.Fatalf("dependency-free direct item = %+v", item)
			}
		}
	}
	if !sawDirect || !sawPrerequisite {
		t.Fatalf("queue did not classify direct/prerequisite items: %+v", queue.Data)
	}

	firstPage, err := f.store.GetReleaseWorkQueue(f.ctx, release.ID, f.actor.ID, ReleaseWorkQueueFilter{Limit: 1})
	if err != nil {
		t.Fatalf("read first queue page: %v", err)
	}
	if firstPage.NextCursor == "" {
		t.Fatalf("first queue page had no continuation: %+v", firstPage)
	}
	newName := "1.4 renamed"
	updatedRelease, err := f.store.UpdateRelease(f.ctx, release.ID, ReleaseInput{Name: &newName}, release.Version, f.actor.ID)
	if err != nil {
		t.Fatalf("update release: %v", err)
	}
	if updatedRelease.Version != release.Version+1 {
		t.Fatalf("updated release version=%d want=%d", updatedRelease.Version, release.Version+1)
	}
	if _, err := f.store.GetReleaseWorkQueue(f.ctx, release.ID, f.actor.ID, ReleaseWorkQueueFilter{Limit: 1, Cursor: firstPage.NextCursor}); !errors.Is(err, ErrReleaseQueueChanged) {
		t.Fatalf("stale queue cursor error=%v, want ErrReleaseQueueChanged", err)
	}
}

func TestReleaseWorkQueueClassifiesCrossReleaseConflict(t *testing.T) {
	f := newDependencyFixture(t, "RELCONFLICT")
	firstName, secondName := "1.0", "2.0"
	first, err := f.store.CreateRelease(f.ctx, f.project.ID, ReleaseInput{Name: &firstName}, f.actor.ID)
	if err != nil {
		t.Fatalf("create first release: %v", err)
	}
	second, err := f.store.CreateRelease(f.ctx, f.project.ID, ReleaseInput{Name: &secondName}, f.actor.ID)
	if err != nil {
		t.Fatalf("create second release: %v", err)
	}
	dependent, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: dependencyStringPtr("first release task"), ReleaseID: &first.ID}, f.actor.ID)
	if err != nil {
		t.Fatalf("create first release task: %v", err)
	}
	prerequisite, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: dependencyStringPtr("second release prerequisite"), ReleaseID: &second.ID}, f.actor.ID)
	if err != nil {
		t.Fatalf("create second release task: %v", err)
	}
	if _, err := f.store.AddTaskDependency(f.ctx, dependent.ID, prerequisite.ID, dependent.Version, f.actor.ID); err != nil {
		t.Fatalf("add cross-release dependency: %v", err)
	}

	queue, err := f.store.GetReleaseWorkQueue(f.ctx, first.ID, f.actor.ID, ReleaseWorkQueueFilter{Limit: 20})
	if err != nil {
		t.Fatalf("read cross-release queue: %v", err)
	}
	if queue.Summary.Direct != 1 || queue.Summary.Required != 2 || queue.Summary.CrossReleaseConflicts != 1 {
		t.Fatalf("cross-release queue summary = %+v", queue.Summary)
	}
	var foundDependent, foundPrerequisite bool
	for _, item := range queue.Data {
		switch item.Task.ID {
		case dependent.ID:
			if item.Relationship != "direct" || item.Disposition != "cross_release_conflict" || len(item.BlockedBy) != 1 || item.BlockedBy[0].ID != prerequisite.ID {
				t.Fatalf("cross-release dependent item = %+v", item)
			}
			foundDependent = true
		case prerequisite.ID:
			if item.Relationship != "prerequisite" || item.Disposition != "claimable" {
				t.Fatalf("cross-release prerequisite item = %+v", item)
			}
			foundPrerequisite = true
		}
	}
	if !foundDependent || !foundPrerequisite {
		t.Fatalf("cross-release queue omitted expected items: %+v", queue.Data)
	}
}

func TestCompleteReleaseConcurrentWritersHaveOneWinner(t *testing.T) {
	f := newDependencyFixture(t, "RELCOMP")
	releaseName := "1.4"
	release, err := f.store.CreateRelease(f.ctx, f.project.ID, ReleaseInput{Name: &releaseName}, f.actor.ID)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	releaseID := release.ID
	task, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: dependencyStringPtr("completed member"), ReleaseID: &releaseID}, f.actor.ID)
	if err != nil {
		t.Fatalf("create release task: %v", err)
	}
	if _, err := f.store.CompleteTask(f.ctx, task.ID, f.actor.ID, task.Version); err != nil {
		t.Fatalf("complete release task: %v", err)
	}

	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer group.Done()
			_, completeErr := f.store.CompleteRelease(f.ctx, release.ID, release.Version, f.actor.ID)
			results <- completeErr
		}()
	}
	group.Wait()
	close(results)

	successes := 0
	alreadyCompleted := 0
	for completeErr := range results {
		if completeErr == nil {
			successes++
		} else if errors.Is(completeErr, ErrReleaseAlreadyCompleted) {
			alreadyCompleted++
		} else {
			t.Fatalf("concurrent completion error = %v, want release_already_completed", completeErr)
		}
	}
	if successes != 1 || alreadyCompleted != 1 {
		t.Fatalf("concurrent completion results = successes:%d already_completed:%d, want one of each", successes, alreadyCompleted)
	}
}
