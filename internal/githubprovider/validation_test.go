package githubprovider

import "testing"

func TestHistoricalPullRequestIdentity(t *testing.T) {
	for _, raw := range []string{"http://github.com/example/repo/pull/1", "https://github.com.evil.invalid/example/repo/pull/1", "https://user@github.com/example/repo/pull/1", "https://github.com/example/../pull/1", "https://github.com/example/repo/pull/1%2f2"} {
		if _, err := ParsePullRequestURL(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	p, err := ParsePullRequestURL("https://GitHub.com/Example/Repo/pull/42/files?diff=split#x")
	if err != nil || p.Repository != "example/repo" || p.Number != 42 || p.URL != "https://github.com/example/repo/pull/42" {
		t.Fatalf("canonical identity: %+v %v", p, err)
	}
}
