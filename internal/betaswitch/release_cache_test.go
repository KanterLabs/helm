package betaswitch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestListReleasesCachesVerifiedBinariesUntilTheyChange(t *testing.T) {
	fixture := newFixture(t, func(context.Context, string, string) error { return nil })
	defer fixture.broker.Close()
	writeRelease(t, fixture.releases, alternateSHA, "refs/heads/beta")
	dir := filepath.Join(fixture.releases, alternateSHA)
	if response, err := fixture.broker.ListReleases(context.Background()); err != nil || len(response.Releases) != 2 {
		t.Fatalf("initial listing = %#v, %v", response, err)
	}
	fixture.broker.binaryMu.Lock()
	cached := len(fixture.broker.binaryCache)
	fixture.broker.binaryMu.Unlock()
	if cached != 4 {
		t.Fatalf("cached binaries = %d, want helm and codex for both releases", cached)
	}

	// Replacing the bytes (same size, new ctime) must invalidate the cache
	// and hide the now-mismatched release from the listing.
	writeFile(t, filepath.Join(dir, "codex"), []byte("CODEX binary"), 0755)
	if response, err := fixture.broker.ListReleases(context.Background()); err != nil || len(response.Releases) != 1 || response.Releases[0].SHA != testSHA {
		t.Fatalf("tampered listing = %#v, %v", response, err)
	}
	// A switch always re-hashes, independent of the listing cache.
	if _, err := fixture.broker.findRelease(alternateSHA); err == nil {
		t.Fatal("findRelease accepted a binary with a mismatched checksum")
	}
	if _, err := os.Stat(filepath.Join(dir, "codex")); err != nil {
		t.Fatal(err)
	}
}
