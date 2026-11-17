package prlinks

import "testing"

// Failure modes: unsafe schemes/userinfo/controls/encoded or ambiguous paths;
// invalid hosts, route/provider mismatch and invalid/overflowing PR numbers;
// duplicate identity from case, query, fragments, port and detail subpaths.
func TestParse(t *testing.T) {
	for _, tc := range []struct {
		raw, provider, host, repo, canonical string
		number                               int
	}{
		{"https://GitHub.com/ACME/Widget/pull/0007/files?x=y#diff", "github", "github.com", "acme/widget", "https://github.com/acme/widget/pull/7", 7},
		{"https://forge.example:8443/ACME/Widget/pulls/12/commits?x=y#x", "gitea", "forge.example:8443", "acme/widget", "https://forge.example:8443/acme/widget/pulls/12", 12},
		{"https://forge.example:443/acme/widget/pulls/12/", "gitea", "forge.example", "acme/widget", "https://forge.example/acme/widget/pulls/12", 12},
	} {
		got, err := Parse(tc.raw)
		if err != nil || got.Provider != tc.provider || got.Host != tc.host || got.Repository != tc.repo || got.URL != tc.canonical || got.Number != tc.number {
			t.Errorf("Parse(%q)=%+v %v", tc.raw, got, err)
		}
	}
	for _, raw := range []string{
		"http://github.com/acme/widget/pull/7", "javascript:alert(1)", "https://me@github.com/acme/widget/pull/7", "https://github.com/acme/widget/pull/0", "https://github.com/acme/widget/pull/-1", "https://github.com/acme/widget/pull/999999999999999999999999", "https://github.com/acme/widget/pulls/7", "https://forge.example/acme/widget/pull/7", "https://github.com/acme%2fother/widget/pull/7", "https://github.com/acme/widget/pull/7%2ffiles", "https://github.com/acme/../pull/7", "https://github.com/acme/widget/pull/7\\files", "https://github.com/acme/widget/pull/7\n", "https://github.com:8443/acme/widget/pull/7", "https://forge.example:0/acme/widget/pulls/7", "https://-bad.example/acme/widget/pulls/7", "https://forge.example/acme/widget/issues/7", "https://forge.example//widget/pulls/7", "https://github.com/acme/widget/pull/7/../8",
	} {
		if got, err := Parse(raw); err == nil {
			t.Errorf("accepted %q: %+v", raw, got)
		}
	}
}
