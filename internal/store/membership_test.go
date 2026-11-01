package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/db"
)

func TestMembershipLifecycleAndLastAdministratorGuard(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	data := New(database)
	email := "owner@example.test"
	owner, err := data.CreateFirstAdmin(ctx, Actor{Kind: "human", Name: "Owner", Email: &email}, "owner-hash")
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	if owner.WorkspaceRole != WorkspaceRoleOwner || !owner.Admin {
		t.Fatalf("first admin = %#v", owner)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("OPS"), Name: stringPtrForTest("Operations")}, owner.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	memberEmail := "member@example.test"
	member, err := data.CreateMember(ctx, Actor{Kind: "human", Name: "Member", Email: &memberEmail}, WorkspaceRoleMember, []string{project.Key}, "member-hash", owner.ID)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if member.WorkspaceRole != WorkspaceRoleMember || member.Admin || len(member.ProjectIDs) != 1 || member.ProjectIDs[0] != project.ID {
		t.Fatalf("member = %#v", member)
	}
	memberID := member.ID
	if _, err := data.CreateMember(ctx, Actor{Kind: "human", Name: "Bad grant", Email: stringPtrForTest("bad@example.test")}, WorkspaceRoleMember, []string{"missing-project"}, "", owner.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid project grant error = %v, want invalid", err)
	}
	if _, err := data.FindActorByEmail(ctx, "bad@example.test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid project grant left actor behind: %v", err)
	}

	adminEmail := "admin@example.test"
	admin, err := data.CreateMember(ctx, Actor{Kind: "human", Name: "Admin", Email: &adminEmail}, WorkspaceRoleAdmin, nil, "admin-hash", owner.ID)
	if err != nil {
		t.Fatalf("create second administrator: %v", err)
	}
	member, err = data.UpdateMember(ctx, member.ID, MemberUpdate{Role: stringPtrForStoreTest(WorkspaceRoleAdmin), ProjectIDs: &[]string{}}, owner.ID)
	if err != nil {
		t.Fatalf("promote member: %v", err)
	}
	if member.WorkspaceRole != WorkspaceRoleAdmin || len(member.ProjectIDs) != 0 || !member.Admin {
		t.Fatalf("promoted member = %#v", member)
	}

	// With a second administrator present, the original owner can be demoted.
	member, err = data.UpdateMember(ctx, owner.ID, MemberUpdate{Role: stringPtrForStoreTest(WorkspaceRoleMember)}, admin.ID)
	if err != nil {
		t.Fatalf("demote owner with another administrator: %v", err)
	}
	if member.WorkspaceRole != WorkspaceRoleMember || member.Admin {
		t.Fatalf("demoted owner = %#v", member)
	}
	member, err = data.UpdateMember(ctx, memberID, MemberUpdate{Role: stringPtrForStoreTest(WorkspaceRoleMember)}, admin.ID)
	if err != nil {
		t.Fatalf("demote the second administrator for last-admin check: %v", err)
	}
	if _, err := data.UpdateMember(ctx, admin.ID, MemberUpdate{Disabled: boolPtrForStoreTest(true)}, member.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("disable last administrator error = %v, want conflict", err)
	}
	admin, err = data.GetMember(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if admin.DisabledAt != nil || admin.WorkspaceRole != WorkspaceRoleAdmin {
		t.Fatalf("last administrator changed after rejected disable: %#v", admin)
	}
	member, err = data.UpdateMember(ctx, memberID, MemberUpdate{Role: stringPtrForStoreTest(WorkspaceRoleAdmin)}, admin.ID)
	if err != nil {
		t.Fatalf("promote a second administrator before disable: %v", err)
	}
	admin, err = data.DisableMember(ctx, admin.ID, member.ID)
	if err != nil {
		t.Fatalf("disable administrator after promoting member: %v", err)
	}
	if admin.DisabledAt == nil {
		t.Fatal("administrator was not disabled")
	}
	admin, err = data.ReactivateMember(ctx, admin.ID, member.ID)
	if err != nil {
		t.Fatalf("reactivate administrator: %v", err)
	}
	if admin.DisabledAt != nil {
		t.Fatal("administrator remained disabled")
	}
}

func TestLegacyHumanProjectRowsDoNotWidenExplicitMembershipCeiling(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	data := New(database)
	email := "owner@example.test"
	owner, err := data.CreateFirstAdmin(ctx, Actor{Kind: "human", Name: "Owner", Email: &email}, "owner-hash")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("LEGACY"), Name: stringPtrForTest("Legacy")}, owner.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO actor_projects(actor_id, project_id) VALUES (?, ?)`, owner.ID, project.ID); err != nil {
		t.Fatalf("insert retained human grant: %v", err)
	}
	refreshed, err := data.GetActor(ctx, owner.ID)
	if err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if len(refreshed.ProjectIDs) != 0 {
		t.Fatalf("legacy human project rows widened explicit ceiling: %#v", refreshed.ProjectIDs)
	}
}

func TestMembershipSessionsPasswordRotationAndRedactedEvents(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	data := New(database)
	email := "owner@example.test"
	owner, err := data.CreateFirstAdmin(ctx, Actor{Kind: "human", Name: "Owner", Email: &email}, "owner-hash")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	memberEmail := "member@example.test"
	member, err := data.CreateMember(ctx, Actor{Kind: "human", Name: "Member", Email: &memberEmail}, WorkspaceRoleMember, nil, "member-hash", owner.ID)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	first, err := data.CreateSessionWithMetadata(ctx, member.ID, "session-one", time.Now().UTC().Add(time.Hour), "192.0.2.10", "Browser/1")
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	second, err := data.CreateSessionWithMetadata(ctx, member.ID, "session-two", time.Now().UTC().Add(time.Hour), "192.0.2.11", "Browser/2")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	listed, err := data.ListSessions(ctx, member.ID, true)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed sessions = %d, want 2", len(listed))
	}
	for _, session := range listed {
		if session.ID == "" || session.ActorID != member.ID || session.IPAddress == "" || session.UserAgent == "" {
			t.Fatalf("session metadata = %#v", session)
		}
	}
	if _, _, err := data.SessionForToken(ctx, "session-one"); err != nil {
		t.Fatalf("first session before password rotation: %v", err)
	}
	if err := data.SetMemberPasswordExcept(ctx, member.ID, "new-member-hash", member.ID, first.ID); err != nil {
		t.Fatalf("rotate member password: %v", err)
	}
	if _, _, err := data.SessionForToken(ctx, "session-one"); err != nil {
		t.Fatalf("current session was revoked by password rotation: %v", err)
	}
	if _, _, err := data.SessionForToken(ctx, "session-two"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other session after password rotation = %v, want not found", err)
	}
	if err := data.RevokeSession(ctx, member.ID, first.ID, member.ID); err != nil {
		t.Fatalf("self revoke current session: %v", err)
	}
	if _, _, err := data.SessionForToken(ctx, "session-one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked session lookup = %v, want not found", err)
	}
	if err := data.RevokeSession(ctx, member.ID, second.ID, owner.ID); err != nil {
		t.Fatalf("admin revoke session: %v", err)
	}

	rows, err := database.QueryContext(ctx, `SELECT type, payload FROM events WHERE type IN ('session.created', 'session.revoked', 'member.password_changed') ORDER BY cursor`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seenTypes := map[string]bool{}
	for rows.Next() {
		var eventType, payload string
		if err := rows.Scan(&eventType, &payload); err != nil {
			t.Fatal(err)
		}
		seenTypes[eventType] = true
		for _, secret := range []string{"session-one", "session-two", "member-hash", "new-member-hash", "192.0.2.10", "Browser/1", "token_hash", "password_hash"} {
			if strings.Contains(payload, secret) {
				t.Fatalf("event %s leaked %q in payload %s", eventType, secret, payload)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{"session.created", "session.revoked", "member.password_changed"} {
		if !seenTypes[eventType] {
			t.Fatalf("missing durable %s event", eventType)
		}
	}
}

func TestSessionForeignKeyAndLegacyShapeRemainValid(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Legacy member", Email: stringPtrForTest("legacy@example.test")}, "")
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	if err := data.CreateSession(ctx, actor.ID, "legacy-session", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create legacy session: %v", err)
	}
	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE actor_id=?`, actor.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("legacy session count = %d", count)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO sessions(id, actor_id, token_hash, expires_at, created_at, last_seen_at) VALUES ('bad-session', 'missing-actor', 'bad-hash', '2027-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("session foreign key accepted a missing actor")
	}
	if err := data.DeleteSession(ctx, "legacy-session"); err != nil {
		t.Fatalf("delete legacy session: %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE actor_id=?`, actor.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("session row was physically deleted; count = %d", count)
	}
}

func TestDisabledAdministratorCannotRevokeAnotherMemberSession(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	data := New(database)

	ownerEmail := "owner@example.test"
	owner, err := data.CreateFirstAdmin(ctx, Actor{Kind: "human", Name: "Owner", Email: &ownerEmail}, "owner-hash")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	memberEmail := "member@example.test"
	member, err := data.CreateMember(ctx, Actor{Kind: "human", Name: "Member", Email: &memberEmail}, WorkspaceRoleMember, nil, "member-hash", owner.ID)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	adminEmail := "admin@example.test"
	admin, err := data.CreateMember(ctx, Actor{Kind: "human", Name: "Admin", Email: &adminEmail}, WorkspaceRoleAdmin, nil, "admin-hash", owner.ID)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	session, err := data.CreateSessionWithMetadata(ctx, member.ID, "member-session", time.Now().UTC().Add(time.Hour), "192.0.2.20", "Browser/2")
	if err != nil {
		t.Fatalf("create member session: %v", err)
	}
	if _, err := data.DisableMember(ctx, admin.ID, owner.ID); err != nil {
		t.Fatalf("disable administrator: %v", err)
	}
	if err := data.RevokeSession(ctx, member.ID, session.ID, admin.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled administrator revoke = %v, want forbidden", err)
	}
	if _, _, err := data.SessionForToken(ctx, "member-session"); err != nil {
		t.Fatalf("disabled administrator changed member session: %v", err)
	}
}

func stringPtrForStoreTest(value string) *string { return &value }
func boolPtrForStoreTest(value bool) *bool       { return &value }
