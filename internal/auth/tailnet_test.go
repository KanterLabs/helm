package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/store"
)

func tailnetTestClaims(now time.Time) TailnetClaims {
	return TailnetClaims{
		Issuer:     TailnetAssertionIssuer,
		Audience:   "https://beta-helm.home.shanekanterman.dev",
		Subject:    "ShaneKanterman04@github",
		Email:      "owner@example.com",
		Name:       "Owner",
		Method:     http.MethodGet,
		RequestURI: "/api/v1/auth/status?from=test",
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(TailnetAssertionTTL).Unix(),
	}
}

func TestTailnetAssertionRoundTripAndRequestBinding(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	key := []byte("01234567890123456789012345678901")
	assertion, err := SignTailnetAssertionAt(key, tailnetTestClaims(now), now)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewTailnetJWTVerifier(key)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	claims, err := verifier.Verify(context.Background(), assertion)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTailnetIdentity(claims, TailnetAssertionIssuer, claims.Audience, claims.Subject, claims.Email, claims.Method, claims.RequestURI, now); err != nil {
		t.Fatalf("request binding rejected: %v", err)
	}
	if err := ValidateTailnetIdentity(claims, TailnetAssertionIssuer, claims.Audience, claims.Subject, claims.Email, http.MethodPost, claims.RequestURI, now); err == nil {
		t.Fatal("wrong method accepted")
	}
	if err := ValidateTailnetIdentity(claims, TailnetAssertionIssuer, claims.Audience, claims.Subject, claims.Email, "get", claims.RequestURI, now); err == nil {
		t.Fatal("case-folded method accepted")
	}
	if err := ValidateTailnetIdentity(claims, TailnetAssertionIssuer, claims.Audience, claims.Subject, claims.Email, claims.Method, "/api/v1/auth/status", now); err == nil {
		t.Fatal("wrong URI accepted")
	}
}

func TestTailnetAssertionRejectsForgedStaleAndInvalidClaims(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	key := []byte("01234567890123456789012345678901")
	verifier, err := NewTailnetJWTVerifier(key)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	valid, err := SignTailnetAssertionAt(key, tailnetTestClaims(now), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), valid+"x"); err == nil {
		t.Fatal("forged signature accepted")
	}
	staleClaims := tailnetTestClaims(now.Add(-3 * time.Minute))
	staleClaims.ExpiresAt = now.Add(-time.Minute).Unix()
	stale := signTailnetTestPayload(key, staleClaims)
	if _, err := verifier.Verify(context.Background(), stale); err == nil {
		t.Fatal("stale assertion accepted")
	}
	invalid := signTailnetTestPayload(key, TailnetClaims{Issuer: TailnetAssertionIssuer, Audience: "https://example.com", Subject: "", Email: "owner@example.com", Method: "GET", RequestURI: "/", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Second).Unix()})
	if _, err := verifier.Verify(context.Background(), invalid); err == nil {
		t.Fatal("invalid short-lived assertion accepted")
	}
}

func signTailnetTestPayload(key []byte, claims TailnetClaims) string {
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	input := tailnetJWTHeader + "." + encoded
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

type staticTailnetVerifier struct {
	claims TailnetClaims
	err    error
}

func (v staticTailnetVerifier) Verify(context.Context, string) (TailnetClaims, error) {
	return v.claims, v.err
}

func TestManagerTailnetRejectsMissingDuplicateWrongOwnerAndPreservesActor(t *testing.T) {
	data := testStore(t)
	mail := "owner@example.com"
	const existingPasswordHash = "existing-password-hash"
	actor, err := data.CreateActor(context.Background(), store.Actor{Kind: "human", Name: "Existing Owner", Email: &mail}, existingPasswordHash)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := tailnetTestClaims(now)
	manager := NewManagerWithTailnetVerifier(data, config.Config{AuthMode: "tailnet", AdminEmail: mail, TailnetOwnerLogin: claims.Subject, TailnetAudience: claims.Audience}, staticTailnetVerifier{claims: claims})
	missing := httptest.NewRequest(http.MethodGet, claims.RequestURI, nil)
	if _, err := manager.Authenticate(context.Background(), missing); err == nil {
		t.Fatal("missing assertion accepted")
	}
	duplicate := httptest.NewRequest(http.MethodGet, claims.RequestURI, nil)
	duplicate.Header.Add(TailnetAssertionHeader, "one")
	duplicate.Header.Add(TailnetAssertionHeader, "two")
	if _, err := manager.Authenticate(context.Background(), duplicate); err == nil {
		t.Fatal("duplicate assertion accepted")
	}
	wrongOwner := claims
	wrongOwner.Subject = "other@example.com"
	wrong := NewManagerWithTailnetVerifier(data, config.Config{AuthMode: "tailnet", AdminEmail: mail, TailnetOwnerLogin: claims.Subject, TailnetAudience: claims.Audience}, staticTailnetVerifier{claims: wrongOwner})
	request := httptest.NewRequest(http.MethodGet, claims.RequestURI, nil)
	request.Header.Set(TailnetAssertionHeader, "verified")
	if _, err := wrong.Authenticate(context.Background(), request); err == nil {
		t.Fatal("wrong owner accepted")
	}
	request.Header.Set(TailnetAssertionHeader, "verified")
	identity, err := manager.Authenticate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Actor.ID != actor.ID || identity.Actor.EmailValue() != mail {
		t.Fatalf("actor changed: got %#v want %s", identity.Actor, actor.ID)
	}
	_, passwordHash, err := data.GetPasswordHash(context.Background(), mail)
	if err != nil {
		t.Fatal(err)
	}
	if passwordHash != existingPasswordHash {
		t.Fatalf("Tailnet authentication changed existing password hash to %q", passwordHash)
	}
}

func TestManagerTailnetRejectsDisabledExistingActor(t *testing.T) {
	data := testStore(t)
	mail := "owner@example.com"
	disabledAt := "2026-09-09T00:00:00Z"
	actor, err := data.CreateActor(context.Background(), store.Actor{
		Kind:       "human",
		Name:       "Disabled Owner",
		Email:      &mail,
		DisabledAt: &disabledAt,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := tailnetTestClaims(now)
	manager := NewManagerWithTailnetVerifier(data, config.Config{
		AuthMode:          "tailnet",
		AdminEmail:        mail,
		TailnetOwnerLogin: claims.Subject,
		TailnetAudience:   claims.Audience,
	}, staticTailnetVerifier{claims: claims})
	request := httptest.NewRequest(http.MethodGet, claims.RequestURI, nil)
	request.Header.Set(TailnetAssertionHeader, "verified")
	if _, err := manager.Authenticate(context.Background(), request); err == nil {
		t.Fatal("disabled Tailnet actor accepted")
	} else if !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("disabled Tailnet actor error = %v, want forbidden", err)
	}
	stillDisabled, err := data.GetActor(context.Background(), actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillDisabled.DisabledAt == nil || *stillDisabled.DisabledAt != disabledAt {
		t.Fatalf("disabled actor state changed: %#v", stillDisabled)
	}
}

func TestManagerTailnetBearerPreservesScopedBehavior(t *testing.T) {
	data := testStore(t)
	actor, err := data.CreateActor(context.Background(), store.Actor{Kind: "agent", Name: "agent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := data.CreateTokenBy(context.Background(), actor.ID, actor.ID, "test", []string{"tasks:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManagerWithTailnetVerifier(data, config.Config{AuthMode: "tailnet", AdminEmail: "owner@example.com", TailnetOwnerLogin: "owner"}, staticTailnetVerifier{err: context.Canceled})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	identity, err := manager.Authenticate(context.Background(), request)
	if err != nil || !identity.IsToken || identity.Actor.ID != actor.ID {
		t.Fatalf("bearer identity=%#v err=%v", identity, err)
	}
}
