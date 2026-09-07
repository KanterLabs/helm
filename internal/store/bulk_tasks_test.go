package store

import (
	"errors"
	"sync"
	"testing"
)

func bulkTaskPriorityInput(value string) TaskInput {
	return TaskInput{Priority: &value}
}

func TestApplyBulkTaskMutationsPartialAndAtomic(t *testing.T) {
	f := newDependencyFixture(t, "BULKSTORE")
	first := f.task(t, "first")
	second := f.task(t, "second")

	partial, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{
		{Reference: first.Key, ExpectedVersion: first.Version, Operation: "priority", Input: bulkTaskPriorityInput("urgent")},
		{Reference: second.Key, ExpectedVersion: second.Version + 1, Operation: "priority", Input: bulkTaskPriorityInput("high")},
	}, false)
	if err != nil {
		t.Fatalf("partial bulk: %v", err)
	}
	if partial.Results[0].Status != "applied" || partial.Results[1].Status != "conflict" {
		t.Fatalf("partial results = %+v", partial.Results)
	}
	updated, err := f.store.GetTask(f.ctx, first.ID)
	if err != nil {
		t.Fatalf("read partial task: %v", err)
	}
	if updated.Priority != "urgent" || updated.Version != first.Version+1 {
		t.Fatalf("partial mutation = %+v", updated)
	}

	atomic, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "atomic", []BulkTaskMutation{
		{Reference: first.Key, ExpectedVersion: updated.Version, Operation: "priority", Input: bulkTaskPriorityInput("low")},
		{Reference: second.Key, ExpectedVersion: second.Version + 1, Operation: "priority", Input: bulkTaskPriorityInput("urgent")},
	}, false)
	if err != nil {
		t.Fatalf("atomic bulk: %v", err)
	}
	if atomic.Results[0].Status != "skipped" || atomic.Results[1].Status != "conflict" {
		t.Fatalf("atomic results = %+v", atomic.Results)
	}
	rolledBack, err := f.store.GetTask(f.ctx, first.ID)
	if err != nil {
		t.Fatalf("read atomic task: %v", err)
	}
	if rolledBack.Priority != "urgent" || rolledBack.Version != updated.Version {
		t.Fatalf("atomic rollback changed first task = %+v", rolledBack)
	}
}

func TestApplyBulkTaskMutationsHonorsClaimsAndDependencies(t *testing.T) {
	f := newDependencyFixture(t, "BULKGUARD")
	claimed := f.task(t, "claimed")
	other, err := f.store.CreateActor(f.ctx, Actor{Kind: "agent", Name: "other"}, "")
	if err != nil {
		t.Fatalf("create claim actor: %v", err)
	}
	if _, err := f.store.ClaimTask(f.ctx, claimed.ID, other.ID, 0, claimed.Version); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	claimed, err = f.store.GetTask(f.ctx, claimed.ID)
	if err != nil {
		t.Fatalf("reload claimed task: %v", err)
	}
	claimBatch, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{
		{Reference: claimed.Key, ExpectedVersion: claimed.Version, Operation: "priority", Input: bulkTaskPriorityInput("urgent")},
	}, false)
	if err != nil {
		t.Fatalf("claim bulk: %v", err)
	}
	if claimBatch.Results[0].Status != "conflict" || !errors.Is(claimBatch.Results[0].Err, ErrClaimUnavailable) {
		t.Fatalf("claim result = %+v", claimBatch.Results[0])
	}
	lifecycleClaimBatch, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{
		{Reference: claimed.Key, ExpectedVersion: claimed.Version, Operation: "block", RequireClaim: false},
	}, false)
	if err != nil {
		t.Fatalf("foreign-claim lifecycle bulk: %v", err)
	}
	if lifecycleClaimBatch.Results[0].Status != "skipped" || !errors.Is(lifecycleClaimBatch.Results[0].Err, ErrForbidden) {
		t.Fatalf("foreign-claim lifecycle result = %+v, want forbidden", lifecycleClaimBatch.Results[0])
	}

	prerequisite := f.task(t, "prerequisite")
	dependent := f.task(t, "dependent")
	dependent = f.add(t, dependent, prerequisite)
	dependencyBatch, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{
		{Reference: dependent.Key, ExpectedVersion: dependent.Version, Operation: "complete", RequireClaim: false},
	}, false)
	if err != nil {
		t.Fatalf("dependency bulk: %v", err)
	}
	if dependencyBatch.Results[0].Status != "conflict" || !errors.Is(dependencyBatch.Results[0].Err, ErrUnmetDependencies) {
		t.Fatalf("dependency result = %+v", dependencyBatch.Results[0])
	}
	unchanged, err := f.store.GetTask(f.ctx, dependent.ID)
	if err != nil {
		t.Fatalf("read dependency task: %v", err)
	}
	if unchanged.Version != dependent.Version || unchanged.CompletedAt != nil {
		t.Fatalf("dependency rejection changed task = %+v", unchanged)
	}
}

func TestApplyBulkTaskMutationsOptimisticConcurrencyAllowsOneWinner(t *testing.T) {
	f := newDependencyFixture(t, "BULKCONCUR")
	task := f.task(t, "one winner")
	inputs := []BulkTaskMutation{
		{Reference: task.Key, ExpectedVersion: task.Version, Operation: "priority", Input: bulkTaskPriorityInput("urgent")},
	}
	results := make(chan BulkTaskMutationBatch, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			batch, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", inputs, false)
			if err != nil {
				t.Errorf("concurrent bulk: %v", err)
				return
			}
			results <- batch
		}()
	}
	group.Wait()
	close(results)
	applied, conflicts := 0, 0
	for batch := range results {
		if len(batch.Results) != 1 {
			t.Fatalf("concurrent batch results = %+v", batch.Results)
		}
		switch batch.Results[0].Status {
		case "applied":
			applied++
		case "conflict":
			conflicts++
		default:
			t.Fatalf("concurrent status = %q, error=%v", batch.Results[0].Status, batch.Results[0].Err)
		}
	}
	if applied != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes applied=%d conflicts=%d", applied, conflicts)
	}
}

func TestApplyBulkTaskMutationsPreservesHierarchyOnFieldUpdate(t *testing.T) {
	f := newDependencyFixture(t, "BULKHIER")
	parent := f.task(t, "parent")
	childTitle := "child"
	child, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: &childTitle, ParentTaskID: &parent.ID}, f.actor.ID)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	priority := "urgent"
	batch, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{{
		Reference: child.Key, ExpectedVersion: child.Version, Operation: "priority", Input: TaskInput{Priority: &priority},
	}}, false)
	if err != nil {
		t.Fatalf("bulk hierarchy field update: %v", err)
	}
	if len(batch.Results) != 1 || batch.Results[0].Status != "applied" || batch.Results[0].Task == nil {
		t.Fatalf("bulk hierarchy result = %+v", batch.Results)
	}
	updated := batch.Results[0].Task
	if updated.Priority != priority || updated.ParentTaskID == nil || *updated.ParentTaskID != parent.ID {
		t.Fatalf("bulk hierarchy update = %+v, want priority and parent retained", updated)
	}
	loadedParent, err := f.store.GetTask(f.ctx, parent.ID)
	if err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if loadedParent.HierarchySummary.ChildCount != 1 {
		t.Fatalf("parent hierarchy after bulk update = %+v, want one child", loadedParent.HierarchySummary)
	}
}

func TestApplyBulkTaskMutationsHonorsChecklistCompletionPolicy(t *testing.T) {
	f := newChecklistFixture(t, "BULKCHECK", "require")
	before, err := f.store.AddTaskChecklistItem(f.ctx, f.task.ID, ChecklistItemInput{Text: checklistStringPtr("finish verification")}, f.task.Version, f.actor.ID)
	if err != nil {
		t.Fatalf("add checklist item: %v", err)
	}

	batch, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{{
		Reference: before.Key, ExpectedVersion: before.Version, Operation: "complete",
	}}, false)
	if err != nil {
		t.Fatalf("bulk checklist completion: %v", err)
	}
	if len(batch.Results) != 1 || batch.Results[0].Status != "conflict" || !errors.Is(batch.Results[0].Err, ErrChecklistIncomplete) {
		t.Fatalf("bulk checklist result = %+v, want checklist conflict", batch.Results)
	}
	after, err := f.store.GetTask(f.ctx, before.ID)
	if err != nil {
		t.Fatalf("reload rejected completion: %v", err)
	}
	if after.Version != before.Version || after.CompletedAt != nil || after.ChecklistSummary.Open != 1 {
		t.Fatalf("rejected bulk completion changed task: before=%+v after=%+v", before, after)
	}
}
