package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/db"
)

func TestMembershipIndependentConnectionsLastAdmin(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "membership.db")
	first, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	a, b := New(first), New(second)
	email := "owner@example.test"
	owner, err := a.CreateFirstAdmin(ctx, Actor{Name: "Owner", Email: &email}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	email = "admin@example.test"
	admin, err := a.CreateMember(ctx, Actor{Name: "Admin", Email: &email}, "admin", nil, "fixture", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, v := range []struct {
		s  *Store
		id string
	}{{a, owner.ID}, {b, admin.ID}} {
		wg.Add(1)
		go func(s *Store, id string) {
			defer wg.Done()
			<-start
			_, err := s.DisableMember(ctx, id, owner.ID)
			results <- err
		}(v.s, v.id)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatalf("unexpected concurrency error: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	var n int
	if err := first.QueryRow(`SELECT COUNT(*) FROM workspace_memberships m JOIN actors a ON a.id=m.actor_id WHERE m.role IN ('owner','admin') AND a.disabled_at IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("active administrators=%d err=%v", n, err)
	}
}

func TestMembershipRevocationExpiresForLegacyReaders(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := New(database)
	email := "owner@example.test"
	owner, err := s.CreateFirstAdmin(ctx, Actor{Name: "Owner", Email: &email}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"operator", "password", "disable", "logout"} {
		t.Run(reason, func(t *testing.T) {
			session, err := s.CreateSessionWithMetadata(ctx, owner.ID, "synthetic-"+reason, time.Now().Add(time.Hour), "", "fixture")
			if err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "operator":
				err = s.RevokeSession(ctx, owner.ID, session.ID, owner.ID)
			case "password":
				err = s.SetMemberPassword(ctx, owner.ID, "new-fixture", owner.ID)
			case "logout":
				err = s.DeleteSession(ctx, "synthetic-"+reason)
			case "disable":
				email := "other@example.test"
				_, err = s.CreateMember(ctx, Actor{Name: "Other", Email: &email}, "admin", nil, "", owner.ID)
				if err == nil {
					_, err = s.DisableMember(ctx, owner.ID, owner.ID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			var n int
			err = database.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash=? AND expires_at>?`, HashSecret("synthetic-"+reason), now()).Scan(&n)
			if err != nil || n != 0 {
				t.Fatalf("legacy reader accepts revoked session: %d %v", n, err)
			}
			if reason == "disable" {
				_, err = s.ReactivateMember(ctx, owner.ID, owner.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMembershipCredentialSnapshotCannotOutliveResetOrDisable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "credential-race.db")
	first, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	a, b := New(first), New(second)
	email := "owner@example.test"
	owner, err := a.CreateFirstAdmin(ctx, Actor{Name: "Owner", Email: &email}, "old-synthetic-hash")
	if err != nil {
		t.Fatal(err)
	}
	// This expected hash represents verification completed before a concurrent
	// reset. Session creation occurs on a separate database pool afterward.
	if err = b.SetMemberPassword(ctx, owner.ID, "new-synthetic-hash", owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.CreateSessionWithMetadata(ctx, owner.ID, "stale-cookie", time.Now().Add(time.Hour), "", "", "old-synthetic-hash"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale login=%v", err)
	}
	if err = a.SetMemberPasswordExcept(ctx, owner.ID, "stale-self-reset", owner.ID, "", "old-synthetic-hash"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale self password change=%v", err)
	}
	session, err := a.CreateSessionWithMetadata(ctx, owner.ID, "fresh-cookie", time.Now().Add(time.Hour), "", "", "new-synthetic-hash")
	if err != nil {
		t.Fatal(err)
	}
	email = "admin@example.test"
	_, err = b.CreateMember(ctx, Actor{Name: "Admin", Email: &email}, "admin", nil, "", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.DisableMember(ctx, owner.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.CreateSessionWithMetadata(ctx, owner.ID, "disabled-cookie", time.Now().Add(time.Hour), "", "", "new-synthetic-hash"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled login=%v", err)
	}
	if _, err = b.ReactivateMember(ctx, owner.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.SessionForToken(ctx, "fresh-cookie"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reactivation revived session %s: %v", session.ID, err)
	}
}
