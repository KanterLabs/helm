package store

import "testing"

// Failure inventory: normal BlockTaskWithReason stores its reason as a comment;
// omitting it loses concrete context, while an older note can invent a blocker.
func TestGithubAuditCaptureNormalBlockReason(t *testing.T) {
	for i, reason := range []string{"Waiting for synthetic vendor approval.", ""} {
		t.Run([]string{"with-reason", "without-reason"}[i], func(t *testing.T) {
			f := newDependencyFixture(t, "BLOCKCTX")
			task, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: stringPtrForTest("Synthetic blocked task"), Description: stringPtrForTest("Goal: Show dependencies.")}, f.actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err = f.store.BlockTaskWithReason(f.ctx, task.ID, f.actor.ID, task.Version, "Older synthetic note must not leak forward")
			if err != nil {
				t.Fatal(err)
			}
			task, err = f.store.BlockTaskWithReason(f.ctx, task.ID, f.actor.ID, task.Version, reason)
			if err != nil {
				t.Fatal(err)
			}
			sel, err := f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			run, _, err := f.store.StartGithubAudit(f.ctx, f.project.ID, f.actor.ID, GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, RequestKey: "block-context", Fingerprint: "block-context"})
			if err != nil {
				t.Fatal(err)
			}
			capture, err := f.store.GetGithubAuditCapture(f.ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if capture.Tasks[0].Context.Blocker != reason {
				t.Fatalf("blocker context %q expected %q", capture.Tasks[0].Context.Blocker, reason)
			}
		})
	}
}
