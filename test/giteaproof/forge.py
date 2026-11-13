"""The forge side of the proof: HTTP with a cookie jar, fixtures, login, consent, revocation.

Adapted from the WP5 research scripts. Nothing here prints a token, code or password.
"""
import base64
import html
import http.cookiejar
import json
import re
import secrets
import ssl
import subprocess
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def opener(context, jar=None):
    handlers = [NoRedirect(), urllib.request.HTTPSHandler(context=context)]
    if jar is not None:
        handlers.append(urllib.request.HTTPCookieProcessor(jar))
    return urllib.request.build_opener(*handlers)


def call(client, method, url, data=None, headers=None, form=False, timeout=30):
    headers = dict(headers or {})
    body = None
    if data is not None:
        if form:
            body = urllib.parse.urlencode(data).encode()
            headers.setdefault("Content-Type", "application/x-www-form-urlencoded")
        else:
            body = json.dumps(data).encode()
            headers.setdefault("Content-Type", "application/json")
    headers.setdefault("Accept", "application/json")
    request = urllib.request.Request(url, data=body, method=method, headers=headers)
    try:
        with client.open(request, timeout=timeout) as response:
            return response.status, response.headers, response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as error:
        return error.code, error.headers, error.read().decode("utf-8", "replace")


def parse(body):
    try:
        return json.loads(body) if body else None
    except ValueError:
        return None


def hidden_inputs(page, action_part):
    for form in re.findall(r"<form\b.*?</form>", page, flags=re.S | re.I):
        opening = form[: form.find(">") + 1]
        if action_part not in opening:
            continue
        fields = {}
        for tag in re.findall(r"<input\b[^>]*>", form, flags=re.I):
            name = re.search(r'name="([^"]*)"', tag)
            value = re.search(r'value="([^"]*)"', tag)
            kind = re.search(r'type="([^"]*)"', tag)
            if name and kind and kind.group(1).lower() == "hidden":
                fields[name.group(1)] = html.unescape(value.group(1)) if value else ""
        return fields
    return None


def page_csrf(page):
    for pattern in (r'<meta name="_csrf" content="([^"]+)"', r"csrfToken:\s*'([^']+)'"):
        found = re.search(pattern, page)
        if found:
            return html.unescape(found.group(1))
    return None


class Failure(Exception):
    pass


class Forge:
    """One running forge container and its fixtures."""

    def __init__(self, family, container, base, cafile, cli):
        self.family, self.container, self.base, self.cli = family, container, base, cli
        self.context = ssl.create_default_context(cafile=cafile)
        self.plain = opener(self.context)
        self.admin = "proof-admin"
        self.admin_password = secrets.token_urlsafe(18)
        self.reader = "helm-reader"
        self.reader_password = secrets.token_urlsafe(18)
        self.outsider = "outsider"
        self.org = "proof-org"
        self.admin_token = None
        self.reader_id = None
        self.browser_jar = http.cookiejar.CookieJar()
        self.browser = opener(self.context, self.browser_jar)

    # --- admin API --------------------------------------------------------
    def api(self, method, path, data=None, token=None, expect=None):
        status, headers, body = call(self.plain, method, self.base + "/api/v1" + path, data,
                                     {"Authorization": "token " + (token or self.admin_token)})
        if expect is not None and status not in (expect if isinstance(expect, tuple) else (expect,)):
            raise Failure(f"{method} {path} answered {status}, expected {expect}")
        return status, headers, parse(body)

    def version(self):
        status, _, body = call(self.plain, "GET", self.base + "/api/v1/version", timeout=5)
        return (parse(body) or {}).get("version") if status == 200 else None

    def login_page_has(self, marker):
        status, _, page = call(self.plain, "GET", self.base + "/user/login", headers={"Accept": "text/html"})
        return status == 200 and marker in page

    def create_admin(self):
        """The only command-line step: an administrator for fixtures, then its API token."""
        command = ["docker", "exec", "-u", "git", self.container, self.cli, "admin", "user", "create",
                   "--admin", "--username", self.admin, "--password", self.admin_password,
                   "--email", "proof-admin@example.invalid", "--must-change-password=false"]
        done = subprocess.run(command, capture_output=True, timeout=120)
        if done.returncode != 0:
            raise Failure(f"admin user create failed with exit {done.returncode}")
        basic = base64.b64encode(f"{self.admin}:{self.admin_password}".encode()).decode()
        status, _, body = call(self.plain, "POST", f"{self.base}/api/v1/users/{self.admin}/tokens",
                               {"name": "proof-fixtures", "scopes": ["all"]}, {"Authorization": "Basic " + basic})
        token = (parse(body) or {}).get("sha1")
        if status != 201 or not token:
            raise Failure(f"admin token creation answered {status}")
        self.admin_token = token

    def create_fixtures(self):
        """Repositories the reader owns, collaborates on and reaches by team, and two it must not see."""
        for name, password in ((self.reader, self.reader_password), (self.outsider, secrets.token_urlsafe(18))):
            self.api("POST", "/admin/users", {"username": name, "email": f"{name}@example.invalid", "password": password,
                                              "must_change_password": False}, expect=201)
        private = {"private": True, "auto_init": True}
        self.api("POST", f"/admin/users/{self.reader}/repos", {"name": "own-private", **private}, expect=201)
        self.api("POST", f"/admin/users/{self.outsider}/repos", {"name": "collab", **private}, expect=201)
        self.api("POST", f"/admin/users/{self.outsider}/repos", {"name": "hidden", **private}, expect=201)
        self.api("PUT", f"/repos/{self.outsider}/collab/collaborators/{self.reader}", {"permission": "read"}, expect=204)
        self.api("POST", "/orgs", {"username": self.org, "visibility": "public"}, expect=201)
        self.api("POST", f"/orgs/{self.org}/repos", {"name": "team-repo", **private}, expect=201)
        self.api("POST", f"/orgs/{self.org}/repos", {"name": "not-shared", **private}, expect=201)
        units = ["repo.code", "repo.pulls"]
        _, _, team = self.api("POST", f"/orgs/{self.org}/teams", {
            "name": "readers", "units_map": {u: "read" for u in units},
            "includes_all_repositories": False}, expect=201)
        self.api("PUT", f"/teams/{team['id']}/members/{self.reader}", expect=204)
        self.api("PUT", f"/teams/{team['id']}/repos/{self.org}/team-repo", expect=204)
        for hidden in (f"{self.outsider}/hidden", f"{self.org}/not-shared"):
            self.api("GET", f"/repos/{hidden}", expect=200)
        _, _, reader = self.api("GET", f"/users/{self.reader}", expect=200)
        _, _, admin = self.api("GET", f"/users/{self.admin}", expect=200)
        self.reader_id = reader["id"]
        return {"reader_is_admin": bool(reader.get("is_admin")), "admin_is_admin": bool(admin.get("is_admin")),
                "reader_id": reader["id"], "admin_id": admin["id"]}

    expected_repositories = ["helm-reader/own-private", "outsider/collab", "proof-org/team-repo"]
    hidden_repositories = ["outsider/hidden", "proof-org/not-shared"]

    def register_applications(self, redirect_uri):
        ids = {}
        for name, confidential in (("helm-public", False), ("helm-confidential", True)):
            _, _, app = self.api("POST", "/user/applications/oauth2", {
                "name": name, "redirect_uris": [redirect_uri], "confidential_client": confidential}, expect=201)
            if bool(app.get("confidential_client")) != confidential:
                raise Failure(f"{name} registered with the wrong client type")
            ids[name] = app["client_id"]
        return ids["helm-public"], ids["helm-confidential"]

    # --- the browser on the forge ------------------------------------------
    def browser_call(self, method, url, data=None):
        headers = {"Accept": "text/html"}
        if method == "POST":
            headers["Origin"] = self.base
        return call(self.browser, method, url, data, headers, form=data is not None)

    def sign_in(self):
        self.browser_jar.clear()
        _, _, page = self.browser_call("GET", self.base + "/user/login")
        fields = hidden_inputs(page, "/user/login") or {}
        fields.update(user_name=self.reader, password=self.reader_password)
        status, headers, _ = self.browser_call("POST", self.base + "/user/login", fields)
        if status not in (302, 303) or "/user/login" in (headers.get("Location") or ""):
            raise Failure(f"forge login answered {status}")

    def consent(self, authorize_url, grant=True):
        """Follow authorize_url as the signed-in reader; return the redirect target and what was seen."""
        if not authorize_url.startswith(self.base + "/login/oauth/authorize?"):
            raise Failure("authorize_url is not on the probed forge")
        status, headers, page = self.browser_call("GET", authorize_url)
        seen = {"consent_shown": False}
        if status in (302, 303):
            location = headers.get("Location")
        elif status == 200:
            fields = hidden_inputs(page, "/login/oauth/grant")
            if fields is None:
                raise Failure(f"no consent form on the authorize page (HTTP {status})")
            seen["consent_shown"] = True
            seen["consent_scope"] = fields.get("scope")
            fields["granted"] = "true" if grant else "false"
            status, headers, _ = self.browser_call("POST", self.base + "/login/oauth/grant", fields)
            if status not in (302, 303):
                raise Failure(f"consent answered {status}")
            location = headers.get("Location")
        else:
            raise Failure(f"authorize page answered {status}")
        if not location:
            raise Failure("forge gave no redirect")
        return location, seen

    def revoke_grants(self):
        """Remove every authorized application from the reader's settings page, as a user would."""
        url = self.base + "/user/settings/applications"
        _, _, page = self.browser_call("GET", url)
        links = sorted(set(re.findall(r'(/user/settings/applications/oauth2/\d+/revoke/\d+)', page)))
        csrf = page_csrf(page)
        posted = []
        for link in links:
            data = {"_csrf": csrf} if csrf else {}
            status, _, _ = self.browser_call("POST", self.base + link, data)
            posted.append(status)
        _, _, after = self.browser_call("GET", url)
        remaining = re.findall(r'/user/settings/applications/oauth2/\d+/revoke/\d+', after)
        return {"links": len(links), "statuses": posted, "remaining": len(remaining)}

    def refresh_directly(self, client_id, refresh_token):
        """Replay a refresh token at the forge; returns (status, error) and never the body's tokens."""
        status, _, body = call(self.plain, "POST", self.base + "/login/oauth/access_token",
                               {"grant_type": "refresh_token", "client_id": client_id,
                                "refresh_token": refresh_token}, form=True)
        return status, (parse(body) or {}).get("error")
