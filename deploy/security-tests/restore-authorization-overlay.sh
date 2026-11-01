# Exercise the authorization overlay with adversarial state when sqlite3 is
# available: an old backup tries to re-enable an actor with an old password,
# while the live database has a disabled/deleted owner and a newer actor. The
# expected result mirrors the SQL in roadmap-restore.sh and catches accidental
# credential, grant, setup, or quota resurrection.
if command -v sqlite3 >/dev/null 2>&1; then
	auth_fixture="$fixture/auth"
	install -d -m 0700 "$auth_fixture"
	current_db="$auth_fixture/current.db"
	candidate_db="$auth_fixture/candidate.db"
	for database in "$current_db" "$candidate_db"; do
		sqlite3 "$database" <<'SQL'
PRAGMA foreign_keys = ON;
CREATE TABLE actors (id TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL, email TEXT, password_hash TEXT, admin INTEGER NOT NULL DEFAULT 0, disabled_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, description TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX actors_email_unique ON actors(lower(email)) WHERE email IS NOT NULL;
CREATE TABLE auth_setup (id INTEGER PRIMARY KEY CHECK (id = 1), completed_at TEXT NOT NULL);
CREATE TABLE projects (id TEXT PRIMARY KEY);
CREATE TABLE actor_projects (actor_id TEXT NOT NULL REFERENCES actors(id) ON DELETE CASCADE, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, PRIMARY KEY(actor_id, project_id));
CREATE TABLE sessions (id TEXT PRIMARY KEY, actor_id TEXT NOT NULL);
CREATE TABLE tokens (id TEXT PRIMARY KEY, actor_id TEXT NOT NULL);
CREATE TABLE idempotency_keys (actor_id TEXT NOT NULL, key TEXT NOT NULL, PRIMARY KEY(actor_id, key));
CREATE TABLE actor_resource_usage (actor_id TEXT PRIMARY KEY, reserved_bytes INTEGER NOT NULL, updated_at TEXT NOT NULL);
SQL
	done
	sqlite3 "$current_db" <<'SQL'
INSERT INTO actors VALUES ('owner','human','Owner','current@example.test','CURRENT_HASH',1,'2026-08-27T00:00:00Z','old','current','current description');
INSERT INTO actors VALUES ('new','agent','New agent','new@example.test','NEW_HASH',0,NULL,'current','current','new description');
INSERT INTO projects VALUES ('p1');
INSERT INTO projects VALUES ('p2');
INSERT INTO auth_setup VALUES (1,'current-setup');
INSERT INTO actor_projects VALUES ('owner','p1');
INSERT INTO actor_projects VALUES ('new','p2');
INSERT INTO actor_resource_usage VALUES ('owner',17,'current');
INSERT INTO actor_resource_usage VALUES ('new',19,'current');
SQL
	sqlite3 "$candidate_db" <<'SQL'
INSERT INTO actors VALUES ('owner','human','Owner','old@example.test','OLD_HASH',0,NULL,'old','old','old description');
INSERT INTO actors VALUES ('old','agent','Old agent','old-only@example.test','OLD_ONLY_HASH',1,NULL,'old','old','old-only description');
INSERT INTO projects VALUES ('p1');
INSERT INTO projects VALUES ('p2');
INSERT INTO auth_setup VALUES (1,'old-setup');
INSERT INTO actor_projects VALUES ('owner','p2');
INSERT INTO actor_resource_usage VALUES ('owner',999,'old');
INSERT INTO actor_resource_usage VALUES ('old',888,'old');
INSERT INTO sessions VALUES ('s1','owner');
INSERT INTO tokens VALUES ('t1','owner');
INSERT INTO idempotency_keys VALUES ('owner','k1');
SQL
	sqlite3 "$candidate_db" <<SQL
PRAGMA foreign_keys = ON;
ATTACH DATABASE '$current_db' AS current_auth;
BEGIN IMMEDIATE;
UPDATE actors
   SET kind = (SELECT c.kind FROM current_auth.actors AS c WHERE c.id = actors.id),
       name = (SELECT c.name FROM current_auth.actors AS c WHERE c.id = actors.id),
       email = (SELECT c.email FROM current_auth.actors AS c WHERE c.id = actors.id),
       password_hash = (SELECT c.password_hash FROM current_auth.actors AS c WHERE c.id = actors.id),
       admin = (SELECT c.admin FROM current_auth.actors AS c WHERE c.id = actors.id),
       disabled_at = (SELECT c.disabled_at FROM current_auth.actors AS c WHERE c.id = actors.id),
       created_at = (SELECT c.created_at FROM current_auth.actors AS c WHERE c.id = actors.id),
       updated_at = (SELECT c.updated_at FROM current_auth.actors AS c WHERE c.id = actors.id),
       description = (SELECT c.description FROM current_auth.actors AS c WHERE c.id = actors.id)
 WHERE id IN (SELECT id FROM current_auth.actors);
UPDATE actors
   SET email = NULL,
       password_hash = NULL,
       admin = 0,
       disabled_at = COALESCE(disabled_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
 WHERE NOT EXISTS (SELECT 1 FROM current_auth.actors AS c WHERE c.id = actors.id);
INSERT INTO actors (id, kind, name, email, password_hash, admin, disabled_at, created_at, updated_at, description)
SELECT c.id, c.kind, c.name, c.email, c.password_hash, c.admin, c.disabled_at, c.created_at, c.updated_at, c.description
  FROM current_auth.actors AS c
 WHERE NOT EXISTS (SELECT 1 FROM actors AS a WHERE a.id = c.id);
DELETE FROM auth_setup;
INSERT INTO auth_setup (id, completed_at) SELECT id, completed_at FROM current_auth.auth_setup;
DELETE FROM actor_projects;
INSERT INTO actor_projects (actor_id, project_id)
SELECT c.actor_id, c.project_id FROM current_auth.actor_projects AS c
JOIN actors AS a ON a.id = c.actor_id JOIN projects AS p ON p.id = c.project_id;
CREATE TABLE IF NOT EXISTS actor_resource_usage (actor_id TEXT PRIMARY KEY, reserved_bytes INTEGER NOT NULL, updated_at TEXT NOT NULL);
DELETE FROM actor_resource_usage;
INSERT INTO actor_resource_usage (actor_id, reserved_bytes, updated_at)
SELECT c.actor_id, c.reserved_bytes, c.updated_at FROM current_auth.actor_resource_usage AS c
JOIN actors AS a ON a.id = c.actor_id;
DELETE FROM sessions;
DELETE FROM tokens;
DELETE FROM idempotency_keys;
COMMIT;
DETACH DATABASE current_auth;
SQL
	owner_result=$(sqlite3 -separator '|' "$candidate_db" "SELECT password_hash,admin,disabled_at,email,description FROM actors WHERE id='owner';")
	[[ "$owner_result" = 'CURRENT_HASH|1|2026-08-27T00:00:00Z|current@example.test|current description' ]] \
		|| fail 'restore auth regression resurrected old owner credentials or state'
	old_password=$(sqlite3 "$candidate_db" "SELECT COALESCE(password_hash,'') FROM actors WHERE id='old';")
	old_admin=$(sqlite3 "$candidate_db" "SELECT admin FROM actors WHERE id='old';")
	old_disabled=$(sqlite3 "$candidate_db" "SELECT COALESCE(disabled_at,'') FROM actors WHERE id='old';")
	old_email=$(sqlite3 "$candidate_db" "SELECT COALESCE(email,'') FROM actors WHERE id='old';")
	[[ -z "$old_password" && "$old_admin" = 0 && -n "$old_disabled" && -z "$old_email" ]] \
		|| fail 'restore auth regression left old-only actor usable'
	new_result=$(sqlite3 -separator '|' "$candidate_db" "SELECT password_hash,admin,COALESCE(disabled_at,''),email FROM actors WHERE id='new';")
	[[ "$new_result" = 'NEW_HASH|0||new@example.test' ]] || fail 'restore auth regression dropped current-only actor state'
	[[ "$(sqlite3 "$candidate_db" 'SELECT completed_at FROM auth_setup WHERE id=1;')" = current-setup ]] \
		|| fail 'restore auth regression resurrected stale auth setup'
	[[ "$(sqlite3 -separator '|' "$candidate_db" 'SELECT actor_id,project_id FROM actor_projects ORDER BY actor_id,project_id;')" = $'new|p2\nowner|p1' ]] \
		|| fail 'restore auth regression resurrected stale project grants'
	[[ "$(sqlite3 -separator '|' "$candidate_db" 'SELECT actor_id,reserved_bytes FROM actor_resource_usage ORDER BY actor_id;')" = $'new|19\nowner|17' ]] \
		|| fail 'restore auth regression resurrected stale quota state'
	[[ "$(sqlite3 "$candidate_db" 'SELECT (SELECT COUNT(*) FROM sessions) || "|" || (SELECT COUNT(*) FROM tokens) || "|" || (SELECT COUNT(*) FROM idempotency_keys);')" = '0|0|0' ]] \
		|| fail 'restore auth regression left replayable credentials'
	printf 'restore_auth_runtime_test=ok\n'
else
	printf 'restore_auth_runtime_test=skipped (requires sqlite3)\n'
fi

# The online backup helper must work from an arbitrary cwd. Run this only in
# an actual root/integration environment where the production service user and
# sqlite3 are available; static checks above still run on ordinary developers'
# machines.
if [[ "$(id -u)" -eq 0 && -n "$(id -u roadmap 2>/dev/null || true)" && $(command -v sqlite3 >/dev/null 2>&1; echo $?) -eq 0 ]]; then
	backup_fixture="$fixture/backup-state"
	install -d -m 0755 -o root -g root "$backup_fixture"
	install -d -m 0750 -o roadmap -g roadmap "$backup_fixture/data"
	install -d -m 0700 -o root -g root "$backup_fixture/backups"
	sqlite3 "$backup_fixture/data/roadmap.db" 'CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL); INSERT INTO schema_migrations VALUES (8, "fixture"); CREATE TABLE smoke (id INTEGER PRIMARY KEY); INSERT INTO smoke VALUES (1);'
	chown roadmap:roadmap "$backup_fixture/data/roadmap.db"
	chmod 0640 "$backup_fixture/data/roadmap.db"
	backup_output=$(cd /tmp && ROADMAP_STATE_DIR="$backup_fixture" ROADMAP_DATA_DIR="$backup_fixture/data" \
		ROADMAP_DB_PATH="$backup_fixture/data/roadmap.db" ROADMAP_BACKUP_DIR="$backup_fixture/backups" \
		ROADMAP_BACKUP_RETENTION=2 "$BACKUP" manual)
	backup_path=${backup_output#backup=}
	[[ -f "$backup_path" && -f "$backup_path.sha256" && -f "$backup_path.metadata" ]] \
		|| fail 'backup helper did not produce complete artifacts outside its backup cwd'
	printf 'backup_runtime_test=ok\n'
else
	printf 'backup_runtime_test=skipped (requires root, roadmap user, and sqlite3)\n'
fi

