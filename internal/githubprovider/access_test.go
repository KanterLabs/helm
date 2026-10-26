package githubprovider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
)

func TestLocalAccessAndGenerationFence(t *testing.T) {
	var requests atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(403)
		fmt.Fprint(w, "denied")
	})
	before := c.Generation()
	if !c.AccessAllowed("example/repo") || c.AccessAllowed("other/repo") {
		t.Fatal("bad local scope")
	}
	if d := c.CachedDiagnostics(); !d.Configured || d.State != "unavailable" {
		t.Fatalf("%+v", d)
	}
	if requests.Load() != 0 {
		t.Fatal("local reads contacted provider")
	}
	if _, err := c.FetchPullRequest(context.Background(), "example/repo", 7); ErrorCode(err) != "permission_required" {
		t.Fatal(err)
	}
	if c.AccessAllowed("example/repo") || c.Generation() == before {
		t.Fatal("permission loss did not fence cache")
	}
	deniedGeneration := c.Generation()
	if err := os.WriteFile(c.tokenFile, []byte("ROTATED_SYNTHETIC_SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	if c.Generation() == deniedGeneration {
		t.Fatal("rotation did not fence cache")
	}
	if err := os.Remove(c.tokenFile); err != nil {
		t.Fatal(err)
	}
	if c.AccessAllowed("example/repo") || c.CachedDiagnostics().Configured {
		t.Fatal("missing token kept access")
	}
	if requests.Load() != 1 {
		t.Fatal("local reads contacted provider")
	}
}
