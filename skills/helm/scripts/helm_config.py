"""Credential loading and safe client error types."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import re
import stat
import sys
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib import error, parse, request

import helm_session



DEFAULT_BASE_URL = "https://tc.shanekanterman.dev"
DEFAULT_CONFIG = Path("~/.config/helm/credentials.json").expanduser()
LEGACY_CONFIG = Path("~/.config/tc-roadmap/credentials.json").expanduser()
MAX_RESPONSE_BYTES = 4 * 1024 * 1024
MAX_TRANSIENT_RETRIES = 2
MAX_RETRY_DELAY_SECONDS = 5.0
# A queue cursor is a snapshot token. If a task, claim, dependency, or
# release changes while following pages, the server asks the caller to start
# over. Keep this bounded so a busy board cannot make a read command loop
# forever.
MAX_RELEASE_QUEUE_RESTARTS = 2
UUID4_PATTERN = re.compile(
    r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$"
)

# Board Audit is intentionally a client-side, read-only projection. Keep the
# limits here independent from the API's larger collection limits so one audit
# result remains useful as agent context even on a busy board.
AUDIT_DEFAULT_STATES = ("backlog", "active")
AUDIT_SEMANTIC_STATES = ("backlog", "ready", "active", "blocked", "completed")
AUDIT_AGENT_STATES = ("working", "waiting", "verifying", "handoff")
AUDIT_PULSE_STALE_SECONDS = 15 * 60
AUDIT_MAX_TEXT = 4000
AUDIT_MAX_AGENT_TEXT = 600
AUDIT_MAX_CRITERION_TEXT = 800
AUDIT_MAX_EVIDENCE_TEXT = 280
AUDIT_MAX_EVIDENCE_ITEMS = 5
AUDIT_MAX_EVIDENCE_REFS = 12


class HelmError(RuntimeError):
    """A safe-to-display Helm client error.

    HTTP failures retain a small amount of structured, non-sensitive context so
    callers can make safe retry decisions without parsing human-facing text.
    """

    def __init__(
        self,
        message: str,
        *,
        status_code: int | None = None,
        error_code: str = "",
        retry_after: str | None = None,
    ) -> None:
        super().__init__(message)
        self.status_code = status_code
        # ``status``/``http_code`` preserve the HTTP status while
        # ``error_code``/``api_code`` preserve the server's machine code.
        self.status = status_code
        self.http_code = status_code
        self.error_code = error_code
        self.api_code = error_code
        self.code = status_code
        self.retry_after = retry_after

    def as_dict(self) -> dict[str, Any]:
        """Return a bounded, machine-readable error without credential data."""

        payload: dict[str, Any] = {
            "code": self.error_code or "client_error",
            "message": str(self)[:300],
        }
        if self.status_code is not None:
            payload["status"] = self.status_code
        if self.retry_after:
            payload["retry_after"] = self.retry_after[:120]
        return payload


# The old exception name is retained for callers that imported the helper
# directly; the canonical public name is HelmError.
RoadmapError = HelmError


@dataclass(frozen=True)
class Config:
    base_url: str
    token: str
    cf_access_client_id: str = ""
    cf_access_client_secret: str = ""
    url_source: str = "provided"
    config_source: str = "provided"


def _credential_path() -> Path:
    canonical = os.environ.get("HELM_CONFIG", "").strip()
    legacy = os.environ.get("TC_ROADMAP_CONFIG", "").strip()
    if canonical and legacy and Path(canonical).expanduser() != Path(legacy).expanduser():
        raise HelmError("conflicting Helm and legacy credential file settings")
    if canonical or legacy:
        return Path(canonical or legacy).expanduser()
    if DEFAULT_CONFIG.exists():
        return DEFAULT_CONFIG
    # Keep the old credential location as a read-only migration fallback.
    return LEGACY_CONFIG


def _read_config_file(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {}
    info = path.stat()
    if info.st_uid != os.getuid():
        raise HelmError(f"credential file is not owned by the current user: {path}")
    if stat.S_IMODE(info.st_mode) & 0o077:
        raise HelmError(f"credential file permissions are too broad: {path}; use mode 0600")
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise HelmError(f"cannot read credential file {path}: {type(exc).__name__}") from exc
    if not isinstance(value, dict):
        raise HelmError(f"credential file must contain a JSON object: {path}")
    return value


def _env_alias(environ: dict[str, str] | None, names: tuple[str, ...], label: str) -> str:
    """Resolve one canonical setting while rejecting conflicting aliases."""

    source = os.environ if environ is None else environ
    values: list[tuple[str, str]] = []
    for name in names:
        raw = str(source.get(name, "")).strip()
        if raw:
            # URL aliases commonly differ only by a trailing slash. Treat
            # those as the same origin before checking for a conflict.
            if name.endswith("URL"):
                raw = raw.rstrip("/")
            values.append((name, raw))
    distinct = {value for _name, value in values}
    if len(distinct) > 1:
        raise HelmError(f"conflicting Helm and legacy {label} settings")
    return values[0][1] if values else ""


def _config_value(values: dict[str, Any], names: tuple[str, ...], label: str) -> str:
    """Read canonical/legacy file keys with the same fail-closed rule."""

    present = []
    for name in names:
        value = values.get(name)
        if isinstance(value, str) and value.strip():
            present.append((name, value.strip().rstrip("/") if name.endswith("url") else value.strip()))
    distinct = {value for _name, value in present}
    if len(distinct) > 1:
        raise HelmError(f"conflicting Helm and legacy {label} settings")
    return present[0][1] if present else ""


def load_config() -> Config:
    path = _credential_path()
    values = _read_config_file(path)
    configured_url = _env_alias(None, ("HELM_URL", "TC_ROADMAP_URL"), "URL")
    configured_token = _env_alias(None, ("HELM_TOKEN", "TC_ROADMAP_TOKEN", "ROADMAP_TOKEN"), "token")
    file_url = _config_value(values, ("helm_url", "base_url", "tc_roadmap_url"), "URL")
    file_token = _config_value(values, ("helm_token", "token", "tc_roadmap_token", "roadmap_token"), "token")
    configured_client_id = _env_alias(
        None, ("HELM_CF_ACCESS_CLIENT_ID", "TC_CF_ACCESS_CLIENT_ID"), "Cloudflare client ID"
    )
    configured_client_secret = _env_alias(
        None, ("HELM_CF_ACCESS_CLIENT_SECRET", "TC_CF_ACCESS_CLIENT_SECRET"), "Cloudflare client secret"
    )
    file_client_id = _config_value(
        values, ("helm_cf_access_client_id", "cf_access_client_id", "tc_cf_access_client_id"), "Cloudflare client ID"
    )
    file_client_secret = _config_value(
        values,
        ("helm_cf_access_client_secret", "cf_access_client_secret", "tc_cf_access_client_secret"),
        "Cloudflare client secret",
    )
    # Environment configuration intentionally has precedence over either
    # credential file, while aliases at the same layer must agree.
    base_url = (configured_url or file_url or DEFAULT_BASE_URL).strip().rstrip("/")
    token = (configured_token or file_token).strip()
    client_id = (configured_client_id or file_client_id).strip()
    client_secret = (configured_client_secret or file_client_secret).strip()
    # urlsplit can raise on malformed IPv6/ports and silently strip controls.
    # Never include unvalidated URL text in diagnostics or exception messages.
    if any(character.isspace() or ord(character) < 32 or ord(character) == 127 for character in base_url):
        raise HelmError("Helm URL must not contain whitespace or control characters")
    try:
        parsed = parse.urlsplit(base_url)
        hostname = parsed.hostname
        parsed.port  # Validate malformed and out-of-range ports before output.
    except ValueError:
        raise HelmError("Helm URL is not a valid HTTP(S) origin") from None
    if parsed.scheme not in {"http", "https"} or not hostname or parsed.username is not None or parsed.password is not None:
        raise HelmError("Helm URL must be an HTTP(S) origin without credentials")
    if parsed.path not in {"", "/"} or parsed.query or parsed.fragment:
        raise HelmError("Helm URL must not contain a path, query, or fragment")
    if parsed.scheme != "https" and parsed.hostname not in {"127.0.0.1", "localhost", "::1"}:
        raise HelmError("Helm URL must use HTTPS except on loopback")
    if not token:
        raise HelmError("Helm agent token is not configured")
    if bool(client_id) != bool(client_secret):
        raise HelmError("both Cloudflare Access credential fields must be configured together")
    if configured_url:
        url_source = "env:" + next(name for name in ("HELM_URL", "TC_ROADMAP_URL") if os.environ.get(name, "").strip())
    elif file_url:
        url_source = "file:" + next(name for name in ("helm_url", "base_url", "tc_roadmap_url") if isinstance(values.get(name), str) and values[name].strip())
    else:
        url_source = "default"
    config_override = next((name for name in ("HELM_CONFIG", "TC_ROADMAP_CONFIG") if os.environ.get(name, "").strip()), "")
    config_source = ("env:" + config_override) if config_override else ("canonical-file" if path == DEFAULT_CONFIG else "legacy-file")
    return Config(base_url, token, client_id, client_secret, url_source, config_source)


