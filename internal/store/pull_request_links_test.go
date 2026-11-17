package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Failure modes: requiring repository selection, leaking legacy snapshots,
// duplicate lifecycle bumps, stale task versions, cross-host identity collision,
// incorrect shared detach, invalid portability and loss through legacy export.
func TestPullRequestLinksOfflineLifecycle(t *testing.T) {
	f := newDependencyFixture(t, "PRL")
	a, b := f.task(t, "first"), f.task(t, "second")
	link, updated, err := f.store.LinkPullRequest(f.ctx, a.ID, "https://github.com/ACME/widgets/pull/7/files?x=y#z", a.Version, f.actor.ID, false)
	if err != nil || updated.Version != a.Version+1 || link.Provider != "github" {
		t.Fatalf("link: %+v %+v %v", link, updated, err)
	}
	duplicate, same, err := f.store.LinkPullRequest(f.ctx, a.ID, link.URL, updated.Version, f.actor.ID, false)
	if err != nil || duplicate.ID != link.ID || same.Version != updated.Version {
		t.Fatalf("duplicate: %+v %v", same, err)
	}
	if _, _, err = f.store.LinkPullRequest(f.ctx, a.ID, link.URL, a.Version, f.actor.ID, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	shared, other, err := f.store.LinkPullRequest(f.ctx, b.ID, link.URL, b.Version, f.actor.ID, false)
	if err != nil || shared.ID != link.ID {
		t.Fatalf("shared: %+v %v", shared, err)
	}
	for _, host := range []string{"one.example", "two.example"} {
		_, updated, err = f.store.LinkPullRequest(f.ctx, a.ID, "https://"+host+"/acme/widgets/pulls/7", updated.Version, f.actor.ID, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.store.DB.ExecContext(f.ctx, `UPDATE github_artifacts SET snapshot_json='not json',verification='verified' WHERE id=?`, link.ID); err != nil {
		t.Fatal(err)
	}
	listed, err := f.store.ListPullRequestLinks(f.ctx, a.ID)
	if err != nil || len(listed) != 3 {
		t.Fatalf("list: %+v %v", listed, err)
	}
	raw, _ := json.Marshal(listed)
	t.Logf("plain link response: %s", raw)
	detached, err := f.store.DetachPullRequest(f.ctx, b.ID, link.ID, other.Version, f.actor.ID, false)
	if err != nil || detached.Version != other.Version+1 {
		t.Fatalf("detach: %+v %v", detached, err)
	}
	listed, err = f.store.ListPullRequestLinks(f.ctx, a.ID)
	if err != nil || len(listed) != 3 {
		t.Fatalf("shared detach: %+v %v", listed, err)
	}
}

func TestPortablePullRequestLinksV5(t *testing.T) {
	ctx := context.Background()
	source, actor, project, task := portableV3Fixture(t)
	link, _, err := source.LinkPullRequest(ctx, task.ID, "https://forge.example:8443/acme/widgets/pulls/7", task.Version, actor.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{2, 3, 4} {
		if _, err = source.ExportPortableVersion(ctx, []string{project.ID}, version); !errors.Is(err, ErrInvalid) {
			t.Fatalf("legacy %d: %v", version, err)
		}
	}
	archive, err := source.ExportPortable(ctx, []string{project.ID})
	if err != nil || archive.Version != 5 {
		t.Fatalf("export: %d %v", archive.Version, err)
	}
	target, owner, _, _ := portableV3Fixture(t)
	report, err := target.ImportPortable(ctx, archive, PortableImportOptions{ActorID: owner.ID})
	if err != nil {
		t.Fatalf("import: %+v %v", report, err)
	}
	var importedTask string
	if err = target.DB.QueryRowContext(ctx, `SELECT ta.task_id FROM task_artifacts ta JOIN github_artifacts a ON a.id=ta.artifact_id WHERE a.url=?`, link.URL).Scan(&importedTask); err != nil {
		t.Fatal(err)
	}
	links, err := target.ListPullRequestLinks(ctx, importedTask)
	if err != nil || len(links) != 1 || links[0].Provider != "gitea" || links[0].Repository != "acme/widgets" || links[0].Host != "forge.example:8443" {
		t.Fatalf("roundtrip: %+v %v", links, err)
	}
	archive.Version = 4
	if _, err = target.ImportPortable(ctx, archive, PortableImportOptions{ActorID: owner.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("v4 Gitea accepted: %v", err)
	}
	t.Logf("v5 retained %s; versions2-4 reject Gitea", link.URL)
}
