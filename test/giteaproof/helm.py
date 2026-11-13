"""Helm's real binary on loopback, driven only through its HTTP API, as the Settings panel would."""
import http.cookiejar
import os
import secrets
import socket
import subprocess
import time
import urllib.parse

from forge import Failure, call, opener, parse

GITEA = "/api/v1/integrations/gitea"
CALLBACK = GITEA + "/oauth/callback"


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


class Helm:
    def __init__(self, binary, data_dir, cafile, pid_file):
        self.binary, self.data_dir, self.cafile, self.pid_file = binary, data_dir, cafile, pid_file
        self.port = free_port()
        self.origin = f"http://127.0.0.1:{self.port}"
        self.db = os.path.join(data_dir, "helm.db")
        self.state_dir = os.path.join(data_dir, "forge")
        self.connection_file = os.path.join(self.state_dir, "gitea.json")
        self.stdout = os.path.join(data_dir, "helm.stdout.log")
        self.stderr = os.path.join(data_dir, "helm.stderr.log")
        self.process = None
        self.starts = 0
        self.sessions = {}

    # --- process ----------------------------------------------------------
    def start(self):
        env = {
            "PATH": "/usr/bin:/bin", "HOME": self.data_dir, "TZ": "UTC",
            "HELM_ADDR": f"127.0.0.1:{self.port}", "HELM_DB": self.db, "HELM_AUTH_MODE": "local",
            "HELM_PUBLIC_ORIGIN": self.origin, "HELM_SECURE_COOKIES": "false", "HELM_DEMO_SEED": "false",
            "HELM_PUBLIC_HOOKS_ADDR": "off", "HELM_GITEA_OAUTH": "on", "HELM_FORGE_STATE_DIR": self.state_dir,
            "SSL_CERT_FILE": self.cafile, "GOMAXPROCS": "2",
        }
        with open(self.stdout, "ab") as out, open(self.stderr, "ab") as err:
            self.process = subprocess.Popen([self.binary], env=env, stdout=out, stderr=err, cwd=self.data_dir,
                                            stdin=subprocess.DEVNULL)
        with open(self.pid_file, "a") as pids:
            pids.write(f"{self.process.pid}\n")
        self.starts += 1
        deadline = time.time() + 30
        while time.time() < deadline:
            if self.process.poll() is not None:
                raise Failure(f"Helm exited during start with {self.process.returncode}")
            try:
                status, _, _ = call(opener(None), "GET", self.origin + "/readyz", timeout=2)
                if status == 200:
                    return self.process.pid
            except OSError:
                pass
            time.sleep(0.3)
        raise Failure("Helm did not become ready")

    def stop(self):
        if self.process and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        code = self.process.returncode if self.process else None
        self.process = None
        return code

    def restart(self):
        old = self.process.pid
        self.stop()
        try:
            os.kill(old, 0)
            raise Failure("old Helm process is still alive after stop")
        except ProcessLookupError:
            pass
        new = self.start()
        if new == old:
            raise Failure("restart reused the PID")
        return old, new

    # --- sessions -----------------------------------------------------------
    def request(self, who, method, path, data=None, origin=True):
        client = self.sessions[who] if who else opener(None)
        headers = {"Origin": self.origin} if origin and method != "GET" else {}
        status, headers, body = call(client, method, self.origin + path, data, headers)
        return status, headers, parse(body)

    def new_session(self, who):
        self.sessions[who] = opener(None, http.cookiejar.CookieJar())

    def set_up_administrators(self):
        """The owner by first-run setup and a second administrator by the members API, each signed in."""
        status, _, body = self.request(None, "GET", "/api/v1/auth/status")
        if status != 200 or not body.get("setup_required") or body.get("mode") != "local":
            raise Failure("Helm on this port is not the fresh instance this run started")
        people = {}
        for who in ("owner", "second"):
            people[who] = {"email": f"{who}@example.invalid", "password": secrets.token_urlsafe(18)}
        self.new_session("owner")
        status, _, _ = self.request("owner", "POST", "/api/v1/auth/setup",
                                    {"email": people["owner"]["email"], "name": "Proof Owner",
                                     "password": people["owner"]["password"]})
        if status != 201:
            raise Failure(f"setup answered {status}")
        self.login("owner", people["owner"])
        status, _, _ = self.request("owner", "POST", "/api/v1/members", {
            "email": people["second"]["email"], "name": "Second Admin", "role": "admin",
            "password": people["second"]["password"]})
        if status != 201:
            raise Failure(f"creating the second administrator answered {status}")
        self.new_session("second")
        self.login("second", people["second"])
        roles = {}
        for who in ("owner", "second"):
            status, _, me = self.request(who, "GET", "/api/v1/auth/me")
            if status != 200:
                raise Failure(f"/auth/me for {who} answered {status}")
            roles[who] = (me.get("id"), me.get("workspace_role"))
        if roles["owner"][0] == roles["second"][0]:
            raise Failure("both sessions are the same actor")
        return {who: role for who, (_, role) in roles.items()}

    def login(self, who, person):
        status, _, _ = self.request(who, "POST", "/api/v1/auth/login",
                                    {"email": person["email"], "password": person["password"]})
        if status != 200:
            raise Failure(f"login for {who} answered {status}")

    # --- the Gitea connection routes ------------------------------------------
    def status(self, who="owner"):
        status, headers, body = self.request(who, "GET", GITEA)
        if status != 200:
            raise Failure(f"status answered {status}")
        if "no-store" not in (headers.get("Cache-Control") or ""):
            raise Failure("status document is cacheable")
        return body

    def probe(self, base_url, who="owner"):
        return self.request(who, "POST", GITEA + "/probe", {"base_url": base_url, "allow_insecure_http": False})

    def start_sign_in(self, base_url, client_id, who="owner"):
        status, _, body = self.request(who, "POST", GITEA + "/oauth/start",
                                       {"base_url": base_url, "client_id": client_id, "allow_insecure_http": False})
        if status != 200 or not (body or {}).get("authorize_url"):
            raise Failure(f"oauth/start answered {status} {(body or {}).get('error', {}).get('code')}")
        return body["authorize_url"]

    def deliver(self, location, who="owner"):
        """Hand the forge's redirect to Helm's callback; return the Settings query Helm redirects to."""
        target = urllib.parse.urlparse(location)
        if f"{target.scheme}://{target.netloc}{target.path}" != self.origin + CALLBACK:
            raise Failure("forge redirected somewhere other than Helm's callback")
        client = self.sessions[who] if who else opener(None)
        status, headers, _ = call(client, "GET", location, headers={"Accept": "text/html"})
        if status != 303:
            raise Failure(f"callback answered {status}")
        if headers.get("Referrer-Policy") != "no-referrer":
            raise Failure("callback redirect lacks Referrer-Policy: no-referrer")
        settled = urllib.parse.urlparse(headers.get("Location") or "")
        if settled.path != "/settings":
            raise Failure("callback did not return to Settings")
        return dict(urllib.parse.parse_qsl(settled.query))

    def check(self, who="owner"):
        status, _, body = self.request(who, "POST", GITEA + "/check")
        if status != 200:
            raise Failure(f"check answered {status}")
        return body

    def repositories(self, who="owner"):
        return self.request(who, "GET", GITEA + "/repositories")

    def disconnect(self, who="owner"):
        return self.request(who, "POST", GITEA + "/disconnect")
