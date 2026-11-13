package forgeconnect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func probeEnv(t *testing.T) (*Manager, *fakeForge) {
	t.Helper()
	m, err := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return m, newFakeForge(t, "")
}

func TestProbeInsecureHTTP(t *testing.T) {
	m, forge := probeEnv(t)
	if _, err := m.Probe(context.Background(), forge.base(), false); Code(err) != CodeInsecureHTTP {
		t.Fatalf("http without opt-in = %v", err)
	}
	if forge.totalCalls() != 0 {
		t.Fatal("refused http still contacted the server")
	}
	server, err := m.Probe(context.Background(), forge.base()+"/", true)
	if err != nil || !server.InsecureHTTP || server.BaseURL != forge.base() || server.Product != "gitea" || server.Version != "28.3.0" {
		t.Fatalf("probe = %+v, %v", server, err)
	}
}

func TestProbeFromDeepPage(t *testing.T) {
	m, forge := probeEnv(t)
	server, err := m.Probe(context.Background(), forge.base()+"/team/repo/pulls/3", true)
	if err != nil || server.BaseURL != forge.base() {
		t.Fatalf("deep page = %+v, %v", server, err)
	}
	if n := forge.count("/team/repo/pulls/3/.well-known/openid-configuration"); n != 1 {
		t.Fatalf("given page not tried first (%d)", n)
	}
}

func TestProbeSubPath(t *testing.T) {
	forge := newFakeForge(t, "/git")
	m, _ := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir()})
	server, err := m.Probe(context.Background(), forge.base()+"/team/repo", true)
	if err != nil || server.BaseURL != forge.base() {
		t.Fatalf("sub-path = %+v, %v", server, err)
	}
	if !strings.HasSuffix(server.BaseURL, "/git") {
		t.Fatalf("settled base lost its path: %q", server.BaseURL)
	}
}

func TestProbeDiscoveryRequirements(t *testing.T) {
	cases := map[string]func(*fakeForge){
		"no authorization_code": func(f *fakeForge) { f.grants = []string{"refresh_token"} },
		"no refresh_token":      func(f *fakeForge) { f.grants = []string{"authorization_code"} },
		"no S256":               func(f *fakeForge) { f.methods = []string{"plain"} },
		"no methods":            func(f *fakeForge) { f.methods = nil },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m, forge := probeEnv(t)
			forge.set(change)
			if _, err := m.Probe(context.Background(), forge.base(), true); Code(err) != CodeNotAForge {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestProbeNotAForge(t *testing.T) {
	bodies := map[string]http.HandlerFunc{
		"404": http.NotFound,
		"html": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<html>hello " + upstreamSentinel + "</html>"))
		},
		"no issuer": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"grant_types_supported":["authorization_code"]}`))
		},
		"500": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, upstreamSentinel, http.StatusInternalServerError)
		},
	}
	for name, handler := range bodies {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			m, _ := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir()})
			_, err := m.Probe(context.Background(), srv.URL+"/a/b", true)
			if Code(err) != CodeNotAForge || strings.Contains(err.Error(), upstreamSentinel) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestProbeOversizedDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"issuer":"` + strings.Repeat("a", 70<<10) + `"}`))
	}))
	defer srv.Close()
	m, _ := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir()})
	if _, err := m.Probe(context.Background(), srv.URL, true); Code(err) != CodeNotAForge {
		t.Fatalf("oversized document = %v", err)
	}
}

func TestProbeRedirectNotFollowed(t *testing.T) {
	forge := newFakeForge(t, "")
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, forge.base()+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer redirector.Close()
	m, _ := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir()})
	if _, err := m.Probe(context.Background(), redirector.URL, true); Code(err) != CodeNotAForge {
		t.Fatalf("redirect = %v", err)
	}
	if forge.totalCalls() != 0 {
		t.Fatal("redirect target was contacted")
	}
}

func TestProbeIssuerVariants(t *testing.T) {
	t.Run("case and trailing slash", func(t *testing.T) {
		m, forge := probeEnv(t)
		forge.set(func(f *fakeForge) { f.issuer = strings.ToUpper(f.base()[:4]) + f.base()[4:] + "/" })
		server, err := m.Probe(context.Background(), forge.base(), true)
		if err != nil || server.BaseURL != forge.base() {
			t.Fatalf("got %+v %v", server, err)
		}
	})
	t.Run("other path on the same host", func(t *testing.T) {
		m, _ := probeEnv(t)
		forge := newFakeForge(t, "/git")
		server, err := m.Probe(context.Background(), forge.srv.URL+"/git/team", true)
		if err != nil || server.BaseURL != forge.base() {
			t.Fatalf("got %+v %v", server, err)
		}
		other := newFakeForge(t, "")
		other.set(func(f *fakeForge) { f.issuer = f.srv.URL + "/git" })
		// A document at the origin pointing at /git, where nothing answers, is refused.
		if _, err := m.Probe(context.Background(), other.srv.URL, true); Code(err) != CodeNotAForge {
			t.Fatalf("issuer path without a document = %v", err)
		}
	})
	t.Run("re-probe of the same host", func(t *testing.T) {
		m, _ := probeEnv(t)
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			issuer := srv.URL + "/git"
			if r.URL.Path == "/git/.well-known/openid-configuration" {
				issuer = srv.URL + "/elsewhere" // never settles: no loop
			}
			writeJSON(w, http.StatusOK, map[string]any{"issuer": issuer, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}})
		}))
		defer srv.Close()
		if _, err := m.Probe(context.Background(), srv.URL, true); Code(err) != CodeNotAForge {
			t.Fatalf("issuer chain = %v", err)
		}
	})
	t.Run("other host", func(t *testing.T) {
		m, forge := probeEnv(t)
		forge.set(func(f *fakeForge) { f.issuer = "https://Gitea.Elsewhere.example/" })
		_, err := m.Probe(context.Background(), forge.base(), true)
		var e *Error
		if Code(err) != CodeIssuerMismatch || !asError(err, &e) || e.Issuer != "https://gitea.elsewhere.example" {
			t.Fatalf("got %#v", err)
		}
		if strings.Contains(err.Error(), "elsewhere") {
			t.Fatal("the issuer belongs in the field, not the error string")
		}
	})
	t.Run("issuer not a URL or too long", func(t *testing.T) {
		for _, issuer := range []string{"not a url " + upstreamSentinel, "https://" + strings.Repeat("a", 260) + ".example", "https://u:p@x.example", "https://x.example/?q=" + upstreamSentinel} {
			m, forge := probeEnv(t)
			forge.set(func(f *fakeForge) { f.issuer = issuer })
			_, err := m.Probe(context.Background(), forge.base(), true)
			if Code(err) != CodeNotAForge {
				t.Fatalf("issuer %.20q = %v", issuer, err)
			}
		}
	})
}

func TestProbeVersion(t *testing.T) {
	m, forge := probeEnv(t)
	forge.set(func(f *fakeForge) { f.version = "17.0.1+gitea-1.22.0" })
	server, err := m.Probe(context.Background(), forge.base(), true)
	if err != nil || server.Product != "forgejo" || server.Version != "17.0.1+gitea-1.22.0" {
		t.Fatalf("forgejo = %+v %v", server, err)
	}
	forge.set(func(f *fakeForge) { f.versionStatus = http.StatusForbidden })
	server, err = m.Probe(context.Background(), forge.base(), true)
	if err != nil || server.Product != "gitea" || server.Version != "" {
		t.Fatalf("refused version = %+v %v", server, err)
	}
	forge.set(func(f *fakeForge) { f.versionStatus = http.StatusOK; f.version = "<script>" + upstreamSentinel })
	server, err = m.Probe(context.Background(), forge.base(), true)
	if err != nil || server.Version != "" {
		t.Fatalf("malformed version = %+v %v", server, err)
	}
}

func TestProbeUnavailableWhenDisabled(t *testing.T) {
	m, _ := New(Options{Enabled: false, StateDir: t.TempDir()})
	if _, err := m.Probe(context.Background(), "gitea.example", false); Code(err) != CodeUnavailable {
		t.Fatalf("disabled probe = %v", err)
	}
}

func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}
