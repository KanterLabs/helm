package forgeconnect

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	good := []struct{ in, want string }{
		{"gitea.example", "https://gitea.example"},
		{"  gitea.example/  ", "https://gitea.example"},
		{"HTTPS://Gitea.Example/Git/", "https://gitea.example/Git"},
		{"https://gitea.example:3000", "https://gitea.example:3000"},
		{"https://10.0.0.5/git", "https://10.0.0.5/git"},
		{"https://127.0.0.1:8443", "https://127.0.0.1:8443"},
		{"https://[fd00::1]:3000", "https://[fd00::1]:3000"},
		{"https://gitea.example/a%20b", "https://gitea.example/a%20b"},
	}
	for _, c := range good {
		got, err := normalizeBaseURL(c.in, false)
		if err != nil || got != c.want {
			t.Errorf("normalizeBaseURL(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	bad := []string{
		"", "   ", "https://user:pass@gitea.example", "https://user@gitea.example",
		"https://gitea.example/?x=1", "https://gitea.example?", "https://gitea.example/#top",
		"ftp://gitea.example", "file:///etc/passwd", "javascript:alert(1)", "gitea.example:abc",
		"https://", "https:///path", "https://gitea.example/a/../b", "https://gitea.example/a//b",
		"https://gitea.example/a/./b", "https://gitea ex.example", "https://gitea.example/\x01",
		"https://" + strings.Repeat("a", 250) + ".example",
		"https://169.254.169.254", "https://[fe80::1]", "https://0.0.0.0", "https://[::]",
		"https://224.0.0.1", "https://255.255.255.255", "https://[ff02::1]", "https://[::ffff:169.254.169.254]",
		"https://gitea.example\\@evil.example",
	}
	for _, in := range bad {
		if _, err := normalizeBaseURL(in, true); Code(err) != CodeInvalidServerURL {
			t.Errorf("normalizeBaseURL(%q) code = %q, want invalid_server_url", in, Code(err))
		}
	}
	if _, err := normalizeBaseURL("http://gitea.example", false); Code(err) != CodeInsecureHTTP {
		t.Fatalf("http without opt-in: %v", err)
	}
	if got, err := normalizeBaseURL("HTTP://gitea.example/", true); err != nil || got != "http://gitea.example" {
		t.Fatalf("http with opt-in = %q, %v", got, err)
	}
}

func TestProbeCandidates(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"gitea.example", []string{"https://gitea.example"}},
		{"gitea.example/git", []string{"https://gitea.example/git", "https://gitea.example"}},
		{"https://gitea.example/org/repo", []string{"https://gitea.example/org/repo", "https://gitea.example/org", "https://gitea.example"}},
		{"https://gitea.example/org/repo/pulls/3", []string{"https://gitea.example/org/repo/pulls/3", "https://gitea.example/org/repo/pulls", "https://gitea.example/org/repo", "https://gitea.example"}},
		{"https://gitea.example/a/b/c/d/e/f", []string{"https://gitea.example/a/b/c/d/e/f", "https://gitea.example/a/b/c/d/e", "https://gitea.example/a/b/c/d", "https://gitea.example"}},
	}
	for _, c := range cases {
		got, err := probeCandidates(c.in, false)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("probeCandidates(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
		if len(got) > 4 {
			t.Errorf("more than 4 candidates for %q", c.in)
		}
	}
}

func TestPublicURLHelpers(t *testing.T) {
	if got := RedirectURI("https://helm.example/"); got != "https://helm.example/api/v1/integrations/gitea/oauth/callback" {
		t.Fatalf("RedirectURI = %q", got)
	}
	if RedirectURI("") != "" || RegisterURL("") != "" || RevokeURL("") != "" {
		t.Fatal("helpers must return empty for empty input")
	}
	if got := RegisterURL("https://gitea.example/git"); got != "https://gitea.example/git/user/settings/applications" {
		t.Fatalf("RegisterURL = %q", got)
	}
	if RevokeURL("https://gitea.example") != "https://gitea.example/user/settings/applications" {
		t.Fatal("RevokeURL")
	}
}

func TestValidClientID(t *testing.T) {
	for _, id := range []string{"a", "0a1b2c3d-4e5f-6789-abcd-ef0123456789", "helm.client_1", strings.Repeat("x", 128)} {
		if !validClientID(id) {
			t.Errorf("rejected %q", id)
		}
	}
	for _, id := range []string{"", " a", "a b", "a/b", "a\n", strings.Repeat("x", 129), "ünï"} {
		if validClientID(id) {
			t.Errorf("accepted %q", id)
		}
	}
}
