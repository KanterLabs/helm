#!/usr/bin/env python3
"""Deploy one exact Coolify service and verify its existing private TLS route."""
from __future__ import annotations

import json
import os
from pathlib import Path
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

from deployment_transition import transition
from rollback_policy import Acceptance, FLOOR_KEY, IMAGE_RE, SHA_RE, PolicyError
from stop_witness import StopWitness

ID_RE = re.compile(r"[A-Za-z0-9_-]{1,128}\Z")


class DeployError(Exception):
    pass


class RejectRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Deployer:
    error_type = DeployError

    def __init__(self):
        def required(key):
            value = os.environ.get(key, "").strip()
            if not value:
                raise DeployError(f"missing environment variable: {key}")
            return value

        self.api = required("COOLIFY_API_URL").rstrip("/")
        self.token = required("COOLIFY_API_TOKEN")
        self.uuid = required("COOLIFY_SERVICE_UUID")
        self.name = required("COOLIFY_SERVICE_NAME")
        self.environment = required("COOLIFY_ENVIRONMENT_UUID")
        self.project = required("COOLIFY_PROJECT_UUID")
        self.server = required("COOLIFY_SERVER_UUID")
        self.site = required("HELM_SITE_URL").rstrip("/")
        self.image = required("HELM_IMAGE")
        self.sha = required("HELM_RELEASE_SHA")
        self.evidence_path = required("HELM_DEPLOY_EVIDENCE")
        self.timeout = int(os.environ.get("COOLIFY_DEPLOYMENT_TIMEOUT", "600"))
        self.poll = int(os.environ.get("COOLIFY_POLL_SECONDS", "5"))
        for identifier in [self.uuid, self.environment, self.project, self.server]:
            if not ID_RE.fullmatch(identifier):
                raise DeployError("invalid Coolify target identifier")
        for url in [self.api, self.site]:
            parsed = urllib.parse.urlsplit(url)
            if (parsed.scheme != "https" or not parsed.hostname or parsed.username
                    or parsed.password or parsed.query or parsed.fragment):
                raise DeployError("API and site URLs must be HTTPS without credentials, query, or fragment")
        if urllib.parse.urlsplit(self.api).path != "/api/v1":
            raise DeployError("COOLIFY_API_URL must end with /api/v1")
        if (not urllib.parse.urlsplit(self.site).hostname.endswith(".home.shanekanterman.dev")
                or urllib.parse.urlsplit(self.site).path):
            raise DeployError("HELM_SITE_URL must be the existing private .home origin")
        if not IMAGE_RE.fullmatch(self.image) or not SHA_RE.fullmatch(self.sha):
            raise DeployError("image must be an immutable Helm GHCR digest and revision a full SHA")
        if not 1 <= self.poll <= 30 or not 30 <= self.timeout <= 1200:
            raise DeployError("invalid deployment polling bounds")
        self.opener = urllib.request.build_opener(RejectRedirects)
        self.evidence = {"service_uuid": self.uuid, "image": self.image, "revision": self.sha}
        target = {"lane": required("HELM_DEPLOY_LANE"), "service_uuid": self.uuid,
                  "service_name": self.name, "project_uuid": self.project,
                  "environment_uuid": self.environment, "server_uuid": self.server,
                  "site_url": self.site, "api_url": self.api,
                  "data_identity": required("HELM_DATA_IDENTITY")}
        self.policy = Acceptance(required("HELM_ACCEPTANCE_BUNDLE"), required("HELM_ACCEPTANCE_SHA256"),
            target, {"image": self.image, "sha": self.sha},
            {"image": required("HELM_RECOVERY_IMAGE"), "sha": required("HELM_RECOVERY_SHA")})
        self.witness = StopWitness(required("HELM_STOP_OBSERVER"),
                                   required("HELM_STOP_OBSERVER_SHA256"), target)
        self.observe_transport = self.witness.transport

    def request(self, method, path, body=None, *, route=False):
        headers = {"Accept": "application/json", "User-Agent": "helm-coolify-deployer/1"}
        if not route:
            headers["Authorization"] = "Bearer " + self.token
        data = None if body is None else json.dumps(body).encode()
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request((self.site if route else self.api) + path,
                                     data=data, method=method, headers=headers)
        operation = ("private route " if route else "Coolify ") + method + " " + path
        try:
            with self.opener.open(req, timeout=20) as response:
                payload = response.read(1_048_577 if not route else 65_537)
            if len(payload) > (1_048_576 if not route else 65_536):
                raise DeployError(operation + " response exceeds limit")
            return json.loads(payload) if payload.strip() else {}
        except urllib.error.HTTPError as exc:
            raise DeployError(operation + f" returned HTTP {exc.code}") from None
        except (urllib.error.URLError, TimeoutError, OSError, ValueError) as exc:
            raise DeployError(operation + " failed or returned invalid JSON") from None

    @property
    def service_path(self):
        return "/services/" + self.uuid

    def service(self):
        value = self.request("GET", self.service_path)
        if not isinstance(value, dict) or value.get("uuid") != self.uuid or value.get("name") != self.name:
            raise DeployError("exact service UUID/name preflight mismatch")
        server = value.get("server")
        if not isinstance(server, dict):
            destination = value.get("destination", {})
            server = destination.get("server") if isinstance(destination, dict) else None
        if not isinstance(server, dict) or server.get("uuid") != self.server:
            raise DeployError("exact service server UUID preflight mismatch")
        apps = value.get("applications")
        if (not isinstance(apps, list) or len(apps) != 1 or not isinstance(apps[0], dict)
                or apps[0].get("name") != "helm" or value.get("databases") != []):
            raise DeployError("service must contain exactly the Helm application and no databases")
        return value

    def preflight(self, *, configuration=True):
        value = self.service()
        env = self.request("GET", f"/projects/{self.project}/{self.environment}")
        if (not isinstance(env, dict) or env.get("uuid") != self.environment
                or not isinstance(env.get("services"), list)):
            raise DeployError("exact environment UUID preflight mismatch")
        matches = [s for s in env["services"] if isinstance(s, dict) and s.get("uuid") == self.uuid]
        if len(matches) != 1 or matches[0].get("name") != self.name:
            raise DeployError("service does not belong to the expected project/environment")
        # The membership lookup above is authoritative even when the service
        # response does not eager-load its environment relationship.
        if env.get("id") is not None and value.get("environment_id") != env["id"]:
            raise DeployError("service environment ID disagrees with UUID membership")
        return self.current() if configuration else None

    def current(self):
        envs = self.request("GET", self.service_path + "/envs")
        if not isinstance(envs, list):
            raise DeployError("invalid service environment response")
        result = {}
        for key, pattern in [("HELM_IMAGE", IMAGE_RE), ("HELM_RELEASE_SHA", SHA_RE), (FLOOR_KEY, None)]:
            matches = [e for e in envs if isinstance(e, dict) and e.get("key") == key]
            if len(matches) != 1 or not isinstance(matches[0].get("value"), str):
                raise DeployError("previous release environment is missing, hidden, or duplicated: " + key)
            value = matches[0]["value"]
            if pattern is not None and not pattern.fullmatch(value):
                raise DeployError("previous release is not immutable: " + key)
            result[key] = value
        self.policy.floor(result[FLOOR_KEY])
        return result

    def patch_env(self, key, value):
        self.policy.fresh()
        if key not in {"HELM_IMAGE", "HELM_RELEASE_SHA", FLOOR_KEY}:
            raise DeployError("unsupported deployment environment mutation")
        self.request("PATCH", self.service_path + "/envs", {
            "key": key, "value": value, "is_literal": True, "is_multiline": False,
        })

    def require_stopped(self, previous):
        self.preflight()
        receipt = self.witness.observe(previous, self.observe_transport)
        # Keep only latest receipt; repeated polling cannot grow evidence.
        self.evidence["stop_witness"] = receipt
        return receipt

    def stop(self, previous):
        self.witness.containers = None
        self.halt()
        self.require_stopped(previous)

    def halt(self):
        # Stopping an exact target is safe even when its floor/coordinates
        # disappeared. This path can never PATCH or start a process.
        self.preflight(configuration=False)
        # Explicitly suppress Coolify's automatic Docker cleanup/image pruning.
        # A queued response or cached exited status is never the barrier.
        status = self.service().get('status')
        if not isinstance(status, str):
            raise DeployError('invalid Coolify status during stop')
        if status not in {'exited', 'stopped', 'exited:unhealthy', 'stopped:unhealthy'}:
            self.request("POST", self.service_path + "/stop?docker_cleanup=false", {})

    def activate(self, pair, previous, floor_raw):
        self.policy.fresh()
        if pair not in [self.policy.candidate, self.policy.recovery]:
            raise DeployError("artifact not eligible for supported recovery")
        self.require_stopped(previous)
        if self.current()[FLOOR_KEY] != floor_raw:
            raise DeployError("authorization floor changed during activation")
        for key, value in [("HELM_IMAGE", pair['image']), ("HELM_RELEASE_SHA", pair['sha'])]:
            self.patch_env(key, value)
        current = self.current()
        if current != {"HELM_IMAGE": pair['image'], "HELM_RELEASE_SHA": pair['sha'], FLOOR_KEY: floor_raw}:
            raise DeployError("Coolify did not retain exact release/floor")
        self.require_stopped(previous)
        self.policy.fresh()
        self.request("POST", self.service_path + "/start", {})

    def wait(self, image, sha):
        deadline = time.monotonic() + self.timeout
        last_error = "service did not become healthy"
        while time.monotonic() < deadline:
            # Check policy before every poll, including unhealthy services and
            # already-current replay. A changed valid floor must not produce
            # successful evidence carrying the pre-start floor checksum.
            current = self.current()
            if current[FLOOR_KEY] != self.floor_snapshot:
                raise PolicyError('authorization floor changed during verification')
            self.policy.provenance(current[FLOOR_KEY], self.witness.checksum)
            if current['HELM_IMAGE'] != image or current['HELM_RELEASE_SHA'] != sha:
                raise DeployError('configured release changed during verification')
            # Target drift is fatal, and API failure must not be hidden by
            # successful route checks against an unrelated workload.
            service = self.service()
            if service.get("status") == "running:healthy":
                try:
                    health = self.request("GET", "/healthz", route=True)
                    ready = self.request("GET", "/readyz", route=True)
                    if (not isinstance(health, dict) or not isinstance(ready, dict)
                            or health.get("status") != "ok" or health.get("service") != "helm"
                            or ready.get("ready") is not True or ready.get("status") != "ok"
                            or ready.get("service") != "helm" or health.get("revision") != sha
                            or ready.get("revision") != sha):
                        raise DeployError("private health/readiness revision mismatch")
                except DeployError as exc:
                    last_error = str(exc)
                else:
                    # Invalid/unknown policy or coordinate drift is immediately
                    # fatal, rather than a reason to keep an uncertain writer up.
                    current = self.current()
                    if current[FLOOR_KEY] != self.floor_snapshot:
                        raise PolicyError('authorization floor changed during route verification')
                    if current["HELM_IMAGE"] != image or current["HELM_RELEASE_SHA"] != sha:
                        raise DeployError("configured release changed during route verification")
                    self.policy.provenance(current[FLOOR_KEY], self.witness.checksum)
                    return {"coolify_status": "running:healthy", "health_revision": sha,
                            "ready_revision": sha, "ready": True}
            time.sleep(self.poll)
        raise DeployError("verification timed out: " + last_error)

    def save(self):
        path = Path(self.evidence_path)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(self.evidence, sort_keys=True, indent=2) + "\n")
        path.chmod(0o600)

    def deploy(self):
        try:
            transition(self)
        except PolicyError as exc:
            self.evidence.update(status="failed-policy-closed", deployment_error=str(exc))
            self.save()
            raise DeployError(str(exc)) from None


def main():
    try:
        deployer = Deployer()
        deployer.deploy()
        print(json.dumps(deployer.evidence, sort_keys=True))
        return 0
    except (DeployError, PolicyError, ValueError, OSError) as exc:
        print("helm-coolify-deploy: " + str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
