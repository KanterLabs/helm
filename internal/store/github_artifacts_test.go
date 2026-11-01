package store

import (
	"errors"
	"fmt"
	"testing"
)

func TestGithubArtifactsSharedLinksAndSelectionFences(t *testing.T) {
	f := newDependencyFixture(t, "GHA")
	a, b := f.task(t, "first"), f.task(t, "second")
	selection, err := f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{{Repository: "acme/widgets", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	artifact, updated, err := f.store.LinkGithubArtifact(f.ctx, a.ID, "acme/widgets", 7, "https://github.com/acme/widgets/pull/7", a.Version, f.actor.ID, false)
	if err != nil || updated.Version != a.Version+1 {
		t.Fatalf("link: %+v %v", updated, err)
	}
	shared, _, err := f.store.LinkGithubArtifact(f.ctx, b.ID, "acme/widgets", 7, artifact.URL, b.Version, f.actor.ID, false)
	if err != nil || shared.ID != artifact.ID {
		t.Fatalf("shared identity: %+v %v", shared, err)
	}
	if _, _, err = f.store.LinkGithubArtifact(f.ctx, a.ID, "acme/widgets", 8, artifact.URL, a.Version, f.actor.ID, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	snap := GithubSnapshot{RepositoryID: 123, Title: "Synthetic pull request", State: "open", HeadSHA: "aaa", BaseSHA: "bbb", ChecksState: "passing", Checks: []GithubCheck{}, ReviewState: "none", FetchedAt: now()}
	if err = f.store.SaveGithubArtifactSnapshot(f.ctx, artifact.ID, selection.Version, &snap, "", false); err != nil {
		t.Fatal(err)
	}
	listed, err := f.store.ListGithubArtifacts(f.ctx, a.ID)
	if err != nil || len(listed) != 1 || listed[0].Snapshot == nil {
		t.Fatalf("list: %+v %v", listed, err)
	}
	if _, err = f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{}, selection.Version); err != nil {
		t.Fatal(err)
	}
	listed, err = f.store.ListGithubArtifacts(f.ctx, a.ID)
	if err != nil || len(listed) != 1 || listed[0].Snapshot != nil {
		t.Fatalf("deselected cache leaked: %+v %v", listed, err)
	}
	if err = f.store.SaveGithubArtifactSnapshot(f.ctx, artifact.ID, selection.Version, &snap, "", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("late write: %v", err)
	}
	detached, err := f.store.DetachGithubArtifact(f.ctx, a.ID, artifact.ID, updated.Version, f.actor.ID, false)
	if err != nil || detached.Version != updated.Version+1 {
		t.Fatalf("detach: %+v %v", detached, err)
	}
	listed, err = f.store.ListGithubArtifacts(f.ctx, b.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("shared detach: %+v %v", listed, err)
	}
	if detached.ColumnID != a.ColumnID {
		t.Fatal("link lifecycle moved task")
	}
}

func TestGithubSelectionVersionAndRepositoryValidation(t *testing.T) {
	f := newDependencyFixture(t, "GHS")
	initial, err := f.store.GetGithubSelection(f.ctx, f.project.ID)
	if err != nil || initial.Version != 1 || initial.Repositories == nil {
		t.Fatalf("initial: %+v %v", initial, err)
	}
	if _, err = f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{{Repository: "acme/widgets", Ref: "main"}, {Repository: "acme/widgets", Ref: "main"}}, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicates: %v", err)
	}
	if _, err = f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{}, 0); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("missing version: %v", err)
	}
}

func TestGithubDisposableSnapshotCacheHasFixedBudget(t *testing.T) {
	f := newDependencyFixture(t, "GCB")
	selection, err := f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{{Repository: "acme/widgets", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Populate existing cache directly so the test checks eviction at the
	// storage boundary without hundreds of unrelated link/HTTP mutations.
	for i := 1; i <= 513; i++ {
		_, err = f.store.DB.ExecContext(f.ctx, `INSERT INTO github_artifacts(id,project_id,repository,number,url,created_at,verification,snapshot_json,cache_selection_version) VALUES(?,?,?,?,?,?,'verified',?,?)`, fmt.Sprintf("cache-%04d", i), f.project.ID, "acme/widgets", i, fmt.Sprintf("https://github.com/acme/widgets/pull/%d", i), now(), `{"fetched_at":"2026-01-01T00:00:00Z","state":"open"}`, selection.Version)
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot := GithubSnapshot{RepositoryID: 123, Title: "Bounded cache", State: "open", HeadSHA: "aaa", BaseSHA: "bbb", FetchedAt: now(), Checks: []GithubCheck{}}
	if err = f.store.SaveGithubArtifactSnapshot(f.ctx, "cache-0513", selection.Version, &snapshot, "", false); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.store.DB.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM github_artifacts WHERE snapshot_json IS NOT NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count > 512 {
		t.Fatalf("snapshot cache budget exceeded: %d", count)
	}
	if err = f.store.DB.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM github_artifacts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 513 {
		t.Fatalf("cache eviction removed durable artifacts: %d", count)
	}
}
