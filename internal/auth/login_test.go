package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/store"
)

func TestLocalLoginAttemptsAreBoundedPerSourceAndAccount(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	data := store.New(database)
	manager := NewManager(data, config.Config{AuthMode: "local", SecureCookies: false})
	if _, err := manager.Setup(ctx, "owner@example.test", "Owner", "correct-password"); err != nil {
		t.Fatalf("setup local auth: %v", err)
	}

	login := func(email, password, remote string) error {
		request := httptest.NewRequest("POST", "http://helm.test/api/v1/auth/login", nil)
		request.RemoteAddr = remote
		_, err := manager.LoginRequest(ctx, email, password, request, httptest.NewRecorder())
		return err
	}
	for attempt := 0; attempt < loginAttemptLimit; attempt++ {
		err := login("owner@example.test", "wrong-password", "203.0.113.10:4000")
		if !errors.Is(err, store.ErrForbidden) || err.Error() != "invalid email or password" {
			t.Fatalf("wrong password attempt %d = %v", attempt+1, err)
		}
	}
	key := "owner@example.test\x00203.0.113.10"
	if got := len(manager.loginAttempts[key]); got != loginAttemptLimit {
		t.Fatalf("attempt count = %d, want %d", got, loginAttemptLimit)
	}
	if err := login("owner@example.test", "wrong-password", "203.0.113.10:4000"); !errors.Is(err, store.ErrForbidden) || err.Error() != "invalid email or password" {
		t.Fatalf("bounded sixth attempt = %v", err)
	}
	if got := len(manager.loginAttempts[key]); got != loginAttemptLimit {
		t.Fatalf("bounded attempt count = %d, want %d", got, loginAttemptLimit)
	}

	// The same account from a different immediate peer has an independent
	// bucket. Forwarded headers are intentionally not consulted by LoginRequest.
	if err := login("owner@example.test", "correct-password", "203.0.113.11:4000"); err != nil {
		t.Fatalf("correct password from independent source = %v", err)
	}
	if got := len(manager.loginAttempts["owner@example.test\x00203.0.113.11"]); got != 0 {
		t.Fatalf("successful login did not clear source bucket: %d", got)
	}

	unknown := login("unknown@example.test", "wrong-password", "203.0.113.12:4000")
	wrong := login("owner@example.test", "wrong-password", "203.0.113.12:4000")
	if unknown == nil || wrong == nil || unknown.Error() != wrong.Error() || unknown.Error() != "invalid email or password" {
		t.Fatalf("credential failures are not generic: unknown=%v wrong=%v", unknown, wrong)
	}
}

func TestLoginAttemptBucketsAreBounded(t *testing.T) {
	manager := &Manager{}
	for index := 0; index < loginAttemptMaxKeys+100; index++ {
		if allowed := manager.reserveLoginAttempt("account-" + strconv.Itoa(index)); allowed != (index < loginAttemptMaxKeys) {
			t.Fatalf("new login bucket %d allowed=%v; full active capacity must fail closed", index, allowed)
		}
	}
	if got := len(manager.loginAttempts); got > loginAttemptMaxKeys {
		t.Fatalf("login attempt buckets = %d, want at most %d", got, loginAttemptMaxKeys)
	}
}

func TestLoginCapacityPreservesActiveLockouts(t *testing.T) {
	manager := &Manager{}
	for attempt := 0; attempt < loginAttemptLimit; attempt++ {
		if !manager.reserveLoginAttempt("victim") {
			t.Fatal("victim rejected before limit")
		}
	}
	if manager.reserveLoginAttempt("victim") {
		t.Fatal("victim limit missing")
	}
	for index := 0; index < loginAttemptMaxKeys+100; index++ {
		manager.reserveLoginAttempt("unrelated-" + strconv.Itoa(index))
	}
	if manager.reserveLoginAttempt("victim") {
		t.Fatal("active lockout evicted by unrelated account traffic")
	}
	manager.loginAttempts["unrelated-0"] = []time.Time{time.Now().Add(-loginAttemptWindow - time.Second)}
	if !manager.reserveLoginAttempt("after-expiry") {
		t.Fatal("expired capacity was not reclaimed")
	}
}
