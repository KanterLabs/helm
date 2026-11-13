"""Drive Helm's real binary through sign-in against one real forge and record each assertion.

Usage: python3 -I -B proof.py <settings.json>  (written by run.sh; holds no secrets)
Prints only states, codes, counts and booleans. Token values stay in memory.
"""
import datetime
import hashlib
import json
import os
import secrets
import stat
import sys
import time
import traceback
import urllib.parse

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from forge import Failure, Forge  # noqa: E402
from helm import CALLBACK, Helm  # noqa: E402

REFRESH_CYCLES = 4
RESTART_AFTER_CYCLE = 2


def say(*parts):
    print(*parts, flush=True)


class Proof:
    def __init__(self, settings):
        self.settings = settings
        self.assertions, self.timings = [], {}
        self.needles = {"access_token": set(), "refresh_token": set(), "code": set()}
        self.forge = Forge(settings["family"], settings["container"], settings["base"], settings["cafile"],
                           settings["cli"])
        self.helm = Helm(settings["binary"], settings["helm_dir"], settings["cafile"], settings["pid_file"])
        self.version = None

    # --- recording ------------------------------------------------------------
    def expect(self, name, ok, detail=""):
        self.assertions.append({"name": name, "ok": bool(ok), "detail": str(detail)})
        say(("PASS" if ok else "FAIL"), name, detail)
        return ok

    def timed(self, name, step, *args):
        began = time.monotonic()
        try:
            return step(*args)
        finally:
            self.timings[name] = round(time.monotonic() - began, 2)

    def connection(self):
        """Read the connection file; remember its tokens for the leak scan; return non-secret facts."""
        with open(self.helm.connection_file) as handle:
            data = json.load(handle)
        for key in ("access_token", "refresh_token"):
            if data.get(key):
                self.needles[key].add(data[key])
        digest = lambda value: hashlib.sha256(value.encode()).hexdigest() if value else ""  # noqa: E731
        return {"access": digest(data.get("access_token")), "refresh": digest(data.get("refresh_token")),
                "expires": data.get("access_expires_at") or "", "refreshed": data.get("refreshed_at") or "",
                "raw_refresh": data.get("refresh_token") or ""}

    def remember_code(self, location):
        code = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(location).query)).get("code")
        if code:
            self.needles["code"].add(code)
        return code

    # --- steps ------------------------------------------------------------------
    def bring_up(self):
        family, base = self.settings["family"], self.settings["base"]
        self.expect("forge_nonce", self.forge.login_page_has(self.settings["nonce"]),
                    "login page carries this run's APP_NAME")
        self.version = self.forge.version() or ""
        tag = self.settings["image"].rsplit(":", 1)[-1]
        right_family = ("+gitea-" in self.version) == (family == "forgejo")
        right_tag = not tag[:1].isdigit() or self.version.startswith(tag)
        self.expect("forge_version", right_family and right_tag, f"{family} {self.version}")
        self.forge.create_admin()
        facts = self.forge.create_fixtures()
        self.expect("reader_not_admin", not facts["reader_is_admin"] and facts["admin_is_admin"]
                    and facts["reader_id"] != facts["admin_id"], "Helm signs in as a non-administrator")
        self.helm.start()
        roles = self.helm.set_up_administrators()
        self.expect("helm_two_admins", roles == {"owner": "owner", "second": "admin"}, json.dumps(roles))
        self.public, self.confidential = self.forge.register_applications(self.helm.origin + CALLBACK)
        doc = self.helm.status()
        self.expect("status_initial", doc["available"] and doc["state"] == "disconnected"
                    and doc["redirect_uri"] == self.helm.origin + CALLBACK, doc["state"])
        port = urllib.parse.urlparse(base).port
        for name, typed in (("probe_full_url", base + "/"), ("probe_bare_host", f"127.0.0.1:{port}"),
                            ("probe_deep_link", base + "/helm-reader/own-private/src/branch/main")):
            status, _, body = self.helm.probe(typed)
            server = (body or {}).get("server") or {}
            self.expect(name, status == 200 and server.get("base_url") == base and server.get("product") == family
                        and server.get("version") == self.version and not server.get("insecure_http"),
                        f"HTTP {status} settled={server.get('base_url') == base} product={server.get('product')}")

    def attempt(self, client_id, who="owner", grant=True, tamper=False):
        """One sign-in attempt; returns Helm's Settings query."""
        authorize = self.helm.start_sign_in(self.settings["base"], client_id)
        state = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(authorize).query)).get("state")
        location, seen = self.forge.consent(authorize, grant=grant)
        self.remember_code(location)
        returned = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(location).query))
        if returned.get("state") != state:
            raise Failure("forge returned a different state than authorize_url carried")
        if seen.get("consent_shown") and seen.get("consent_scope") != "read:repository read:user":
            raise Failure("consent page asked for an unexpected scope")
        if tamper:
            location = location.replace("state=" + urllib.parse.quote(state, safe=""),
                                        "state=" + secrets.token_urlsafe(16))
            if state in location:
                raise Failure("could not forge the state")
        return self.helm.deliver(location, who)

    def failures(self):
        self.forge.sign_in()
        cases = (("client_confidential", dict(client_id=self.confidential), "client_confidential"),
                 ("consent_denied", dict(client_id=self.public, grant=False), "denied"),
                 ("other_admin_state", dict(client_id=self.public, who="second"), "state_invalid"),
                 ("forged_state", dict(client_id=self.public, tamper=True), "state_invalid"),
                 ("no_helm_session", dict(client_id=self.public, who=None), "state_invalid"))
        for name, kwargs, code in cases:
            query = self.attempt(**kwargs)
            self.expect(name, query == {"gitea_error": code}, json.dumps(query))
        self.expect("still_disconnected", self.helm.status()["state"] == "disconnected")

    def sign_in(self, label):
        self.forge.sign_in()
        query = self.attempt(self.public)
        self.expect(f"{label}_redirect", query == {"gitea": "connected"}, json.dumps(query))
        doc = self.helm.status()
        account = doc.get("account") or {}
        self.expect(f"{label}_connected", doc["state"] == "connected" and doc["access"] == "scoped"
                    and account.get("login") == self.forge.reader and account.get("id") == self.forge.reader_id
                    and account.get("login") != self.forge.admin,
                    f"state={doc['state']} access={doc['access']} login={account.get('login')}")
        self.repositories(f"{label}_repositories")

    def repositories(self, name):
        status, _, body = self.helm.repositories()
        names = sorted(r["full_name"] for r in (body or {}).get("repositories") or [])
        self.expect(name, status == 200 and names == sorted(Forge.expected_repositories)
                    and not set(names) & set(Forge.hidden_repositories) and not body.get("truncated"),
                    f"HTTP {status} " + ",".join(names))

    def file_modes(self):
        info, parent = os.lstat(self.helm.connection_file), os.lstat(self.helm.state_dir)
        self.expect("file_mode", stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o600
                    and stat.S_ISDIR(parent.st_mode) and stat.S_IMODE(parent.st_mode) == 0o700
                    and info.st_uid == os.getuid(),
                    f"file {oct(stat.S_IMODE(info.st_mode))} dir {oct(stat.S_IMODE(parent.st_mode))}")

    def refresh_cycles(self):
        for cycle in range(1, REFRESH_CYCLES + 1):
            before = self.connection()
            expires = datetime.datetime.fromisoformat(before["expires"].replace("Z", "+00:00"))
            refreshed = datetime.datetime.fromisoformat(before["refreshed"].replace("Z", "+00:00"))
            if cycle == 1:
                self.expect("short_token_lifetime", (expires - refreshed).total_seconds() <= 30,
                            f"{(expires - refreshed).total_seconds():.0f}s")
            wait = (expires - datetime.datetime.now(datetime.timezone.utc)).total_seconds() + 1.5
            time.sleep(max(0.0, min(wait, 60)))
            doc = self.helm.check()
            after = self.connection()
            self.expect(f"refresh_{cycle}", doc["state"] == "connected" and doc["check"]["state"] == "ok"
                        and after["access"] != before["access"] and after["refresh"] != before["refresh"],
                        f"state={doc['state']} check={doc['check']['state']} rotated="
                        f"{after['access'] != before['access'] and after['refresh'] != before['refresh']}")
            if cycle == 1:
                status, error = self.forge.refresh_directly(self.public, before["raw_refresh"])
                self.expect("one_time_refresh_on", status == 400 and error in ("unauthorized_client", "invalid_grant"),
                            f"replayed superseded refresh token: HTTP {status} {error}")
            if cycle == RESTART_AFTER_CYCLE:
                old, new = self.helm.restart()
                doc = self.helm.status()
                self.expect("restart_keeps_connection", doc["state"] == "connected" and old != new
                            and (doc.get("account") or {}).get("login") == self.forge.reader, doc["state"])
        self.repositories("repositories_after_refresh")

    def revoke_and_recover(self):
        before = self.connection()
        revoked = self.forge.revoke_grants()
        self.expect("forge_revoke", revoked["links"] >= 1 and revoked["remaining"] == 0, json.dumps(revoked))
        doc = self.helm.check()
        self.expect("revoked_reconnect_required", doc["state"] == "reconnect_required", doc["state"])
        with open(self.helm.connection_file) as handle:
            kept = json.load(handle)
        self.expect("revoked_tokens_wiped", not kept.get("access_token") and not kept.get("refresh_token")
                    and before["access"] != "", "server and client ID kept, tokens removed")
        self.sign_in("reconnect")

    def disconnect(self):
        self.connection()
        self.file_modes()
        status, _, doc = self.helm.disconnect()
        self.expect("disconnect", status == 200 and doc["state"] == "disconnected"
                    and not os.path.exists(self.helm.connection_file), f"HTTP {status}")

    # --- secrets ------------------------------------------------------------------
    def leak_scan(self, artifact_dir):
        counts = {k: len(v) for k, v in self.needles.items()}
        enough = all(counts.values()) and all(len(n) >= 20 for v in self.needles.values() for n in v)
        self.expect("leak_scan_has_needles", enough, json.dumps(counts))
        needles = [n.encode() for v in self.needles.values() for n in v]
        leaked = []
        for root in (self.helm.data_dir, artifact_dir):
            for folder, _, files in os.walk(root):
                if os.path.abspath(folder).startswith(os.path.abspath(self.helm.state_dir)):
                    continue
                for name in files:
                    path = os.path.join(folder, name)
                    try:
                        content = open(path, "rb").read()
                    except OSError:
                        continue
                    if any(n in content for n in needles):
                        leaked.append(os.path.relpath(path, root))
        scanned = [n for n in os.listdir(self.helm.data_dir)]
        self.expect("no_secret_in_helm_output_or_sqlite", not leaked and "helm.db" in scanned
                    and "helm.stderr.log" in scanned, "leaked in: " + ",".join(leaked) if leaked else "clean")
        return not leaked


def main():
    with open(sys.argv[1]) as handle:
        settings = json.load(handle)
    proof = Proof(settings)
    started, error = time.monotonic(), None
    try:
        proof.timed("bring_up", proof.bring_up)
        proof.timed("failure_cases", proof.failures)
        proof.timed("sign_in", proof.sign_in, "sign_in")
        proof.connection()
        proof.file_modes()
        proof.timed("refresh_cycles", proof.refresh_cycles)
        proof.timed("revoke_and_recover", proof.revoke_and_recover)
        proof.timed("disconnect", proof.disconnect)
    except Exception as failure:  # noqa: BLE001 - recorded, then the run fails
        error = f"{type(failure).__name__}: {failure}" if isinstance(failure, Failure) else type(failure).__name__
        proof.expect("aborted", False, error)
        if not isinstance(failure, Failure):
            traceback.print_exc(limit=4)
    finally:
        exit_code = proof.helm.stop()
        clean = proof.leak_scan(settings["artifact_dir"])
    if clean:
        for name in ("helm.stdout.log", "helm.stderr.log"):
            path = os.path.join(settings["helm_dir"], name)
            if os.path.exists(path):
                with open(path, "rb") as source, open(os.path.join(settings["artifact_dir"],
                                                                   f"{settings['family']}-{name}"), "wb") as target:
                    target.write(source.read())
    passed = all(a["ok"] for a in proof.assertions) and error is None
    result = {"forge": settings["family"], "image": settings["image"], "version": proof.version,
              "passed": passed, "helm_starts": proof.helm.starts, "helm_exit": exit_code,
              "seconds": round(time.monotonic() - started, 1), "timings": proof.timings,
              "assertions": proof.assertions}
    with open(settings["result"], "w") as handle:
        json.dump(result, handle, indent=1)
    say(f"{settings['family']}: {'PASSED' if passed else 'FAILED'} "
        f"({sum(a['ok'] for a in proof.assertions)}/{len(proof.assertions)} assertions)")
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())
