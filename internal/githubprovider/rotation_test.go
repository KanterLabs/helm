package githubprovider

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

// Failure list: failures.md, "Found by the independent review".

// A request sent with a token that another caller's refresh rotated away gets
// a 401. That is not a lost permission: the fetch is retried once with the
// current token, nothing is denied and the generation stays.
func TestRotatedTokenIsRetriedNotDenied(t *testing.T) {
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	var calls atomic.Int32
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+sourceTokenB {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, repoMetadataJSON)
	})
	// The source renews when told its token was refused, as the manager does.
	src.onRejected = func(string) { src.set(sourceTokenB, sourceIdentity1, nil) }
	generation := c.Generation()
	var metadata map[string]any
	if _, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata); err != nil {
		t.Fatalf("fetch across a token rotation: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("%d requests, want the refused one and one retry", calls.Load())
	}
	if got := src.rejections(); len(got) != 1 || got[0] != sourceTokenA {
		t.Fatalf("rejections: %d", len(got))
	}
	if c.Generation() != generation || !c.AccessAllowed("example/repo") {
		t.Fatal("a rotated token changed the generation or denied the repository")
	}
}

// When the renewed token is refused too, the fetch fails as unavailable. The
// source decides when the sign-in has ended; the provider denies nothing.
func TestRefusedTwiceIsUnavailableNotDenied(t *testing.T) {
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	var calls atomic.Int32
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	src.onRejected = func(token string) {
		if token == sourceTokenA {
			src.set(sourceTokenB, sourceIdentity1, nil)
		}
	}
	generation := c.Generation()
	var metadata map[string]any
	if _, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata); ErrorCode(err) != "unavailable" {
		t.Fatalf("two refusals: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("%d requests, want exactly one retry", calls.Load())
	}
	if got := src.rejections(); len(got) != 2 || got[0] != sourceTokenA || got[1] != sourceTokenB {
		t.Fatalf("rejections: %v", len(got))
	}
	if c.Generation() != generation || !c.AccessAllowed("example/repo") {
		t.Fatal("refused tokens changed the generation or denied the repository")
	}
}
