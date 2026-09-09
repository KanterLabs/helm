package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
)

type mockWhoIs struct {
	peer TailnetPeer
	err  error
	addr string
}

func TestLocalAPIWhoIsUsesUnixLocalAPIHost(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "tailscaled.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "local-tailscaled.sock" {
			t.Errorf("LocalAPI host = %q, want local-tailscaled.sock", r.Host)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path != "/localapi/v0/whois" || r.URL.Query().Get("addr") != "100.124.12.50:4123" {
			t.Errorf("WhoIs request = %s?%s", r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"Node":{"Tags":[]},"UserProfile":{"LoginName":"ShaneKanterman04@github","DisplayName":"Shane"}}`))
	})}
	serveDone := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(serveDone)
	}()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		<-serveDone
	})

	peer, err := (LocalAPIWhoIs{Socket: socket, Timeout: time.Second}).WhoIs(context.Background(), "100.124.12.50:4123")
	if err != nil {
		t.Fatal(err)
	}
	if peer.LoginName != "ShaneKanterman04@github" || peer.DisplayName != "Shane" || len(peer.Tags) != 0 {
		t.Fatalf("WhoIs peer = %#v", peer)
	}
}

func (m *mockWhoIs) WhoIs(_ context.Context, addr string) (TailnetPeer, error) {
	m.addr = addr
	return m.peer, m.err
}

func forwardAuthRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:44321"
	request.Header.Set(forwardedMethodHeader, http.MethodGet)
	request.Header.Set(forwardedURIHeader, "/api/v1/auth/status?from=tailnet")
	request.Header.Set(remoteAddrHeader, "100.124.12.50:4123")
	return request
}

func TestForwardAuthSignsOwnerAndBindsRequest(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	mock := &mockWhoIs{peer: TailnetPeer{LoginName: "ShaneKanterman04@github", DisplayName: "Shane"}}
	handler, err := NewHandlerWithAdminEmail(key, "ShaneKanterman04@github", "owner@example.com", "https://beta-helm.home.shanekanterman.dev", mock)
	if err != nil {
		t.Fatal(err)
	}
	request := forwardAuthRequest("")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("response = %d body=%q", response.Code, response.Body.String())
	}
	assertion := response.Header().Get(auth.TailnetAssertionHeader)
	if assertion == "" {
		t.Fatal("success response omitted assertion")
	}
	verifier, err := auth.NewTailnetJWTVerifier(key)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(context.Background(), assertion)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "ShaneKanterman04@github" || claims.Email != "owner@example.com" || claims.RequestURI != "/api/v1/auth/status?from=tailnet" {
		t.Fatalf("claims = %#v", claims)
	}
	if mock.addr != "100.124.12.50:4123" {
		t.Fatalf("WhoIs address = %q", mock.addr)
	}
}

func TestForwardAuthRejectsForgedHeadersAndPeers(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	cases := []struct {
		name string
		edit func(*http.Request, *mockWhoIs)
		want int
	}{
		{name: "non-loopback caller", edit: func(r *http.Request, _ *mockWhoIs) { r.RemoteAddr = "10.0.0.5:443" }, want: http.StatusForbidden},
		{name: "duplicate method", edit: func(r *http.Request, _ *mockWhoIs) { r.Header.Add(forwardedMethodHeader, http.MethodPost) }, want: http.StatusBadRequest},
		{name: "incoming assertion", edit: func(r *http.Request, _ *mockWhoIs) { r.Header.Set(auth.TailnetAssertionHeader, "smuggled") }, want: http.StatusBadRequest},
		{name: "tagged node", edit: func(_ *http.Request, m *mockWhoIs) { m.peer.Tags = []string{"tag:server"} }, want: http.StatusUnauthorized},
		{name: "wrong owner", edit: func(_ *http.Request, m *mockWhoIs) { m.peer.LoginName = "other@example.com" }, want: http.StatusUnauthorized},
		{name: "unknown node", edit: func(_ *http.Request, m *mockWhoIs) { m.err = errUnknownPeer }, want: http.StatusUnauthorized},
		{name: "oversized body", edit: func(_ *http.Request, _ *mockWhoIs) {}, want: http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockWhoIs{peer: TailnetPeer{LoginName: "ShaneKanterman04@github"}}
			handler, err := NewHandlerWithAdminEmail(key, "ShaneKanterman04@github", "owner@example.com", "https://beta-helm.home.shanekanterman.dev", mock)
			if err != nil {
				t.Fatal(err)
			}
			body := ""
			if tc.name == "oversized body" {
				body = strings.Repeat("x", maxForwardAuthBody+1)
			}
			request := forwardAuthRequest(body)
			tc.edit(request, mock)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d", response.Code, tc.want)
			}
		})
	}
}

func TestForwardAuthRejectsInvalidInputAndResolverFailure(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	mock := &mockWhoIs{peer: TailnetPeer{LoginName: "ShaneKanterman04@github"}, err: errors.New("socket unavailable")}
	handler, err := NewHandlerWithAdminEmail(key, "ShaneKanterman04@github", "owner@example.com", "https://beta-helm.home.shanekanterman.dev", mock)
	if err != nil {
		t.Fatal(err)
	}
	request := forwardAuthRequest("")
	request.Header.Del(remoteAddrHeader)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing address status = %d", response.Code)
	}
	request = forwardAuthRequest("")
	request.Header.Set(forwardedURIHeader, "https://evil.example/")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("absolute URI status = %d", response.Code)
	}
	request = forwardAuthRequest("")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("resolver failure status = %d", response.Code)
	}
}

func TestForwardAuthShortAssertionLifetime(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	mock := &mockWhoIs{peer: TailnetPeer{LoginName: "ShaneKanterman04@github"}}
	handler, err := NewHandlerWithAdminEmail(key, "ShaneKanterman04@github", "owner@example.com", "https://beta-helm.home.shanekanterman.dev", mock)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, forwardAuthRequest(""))
	verifier, _ := auth.NewTailnetJWTVerifier(key)
	claims, err := verifier.Verify(context.Background(), response.Header().Get(auth.TailnetAssertionHeader))
	if err != nil {
		t.Fatal(err)
	}
	if lifetime := time.Unix(claims.ExpiresAt, 0).Sub(time.Unix(claims.IssuedAt, 0)); lifetime != auth.TailnetAssertionTTL {
		t.Fatalf("assertion lifetime = %s", lifetime)
	}
}
