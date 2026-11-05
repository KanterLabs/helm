"""Bounded JSON HTTP transport and safe diagnostics."""

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



class _NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        return None


class Client:
    def __init__(self, config: Config) -> None:
        self.config = config

    @staticmethod
    def _retry_safe(method: str, path: str, idempotency_key: str | None) -> bool:
        """Return whether a request can be replayed after a transient error.

        GET is naturally replayable. Normal writes are replayable only when
        they carry the helper's deterministic idempotency key. Heartbeat is a
        deliberately idempotent server-side timestamp touch and therefore is
        safe even though it intentionally has no idempotency key.
        """

        if method.upper() in {"GET", "HEAD", "OPTIONS"}:
            return True
        return bool(idempotency_key) or (
            method.upper() == "POST" and path.rstrip("/").endswith("/heartbeat")
        )

    @staticmethod
    def _retry_delay(headers: dict[str, str], attempt: int) -> float:
        """Choose a bounded delay while preserving the server's hint.

        ``Retry-After`` can be either a number of seconds or an HTTP date. A
        malformed/far-future value falls back to a small exponential delay;
        no caller is ever made to sleep for more than five seconds.
        """

        raw = headers.get("retry-after", "").strip()
        delay: float | None = None
        if raw:
            try:
                delay = max(0.0, float(raw))
            except ValueError:
                try:
                    retry_at = datetime.strptime(raw, "%a, %d %b %Y %H:%M:%S GMT").replace(tzinfo=timezone.utc)
                    delay = max(0.0, (retry_at - datetime.now(timezone.utc)).total_seconds())
                except ValueError:
                    delay = None
        if delay is None:
            delay = min(0.5 * (2**attempt), MAX_RETRY_DELAY_SECONDS)
        return min(delay, MAX_RETRY_DELAY_SECONDS)

    def call(
        self,
        method: str,
        path: str,
        *,
        body: dict[str, Any] | None = None,
        if_match: int | None = None,
        idempotency_key: str | None = None,
    ) -> tuple[Any, dict[str, str]]:
        data = None
        if body is not None:
            data = json.dumps(body, separators=(",", ":")).encode("utf-8")
        target = self.config.base_url + "/api/v1" + path
        retry_safe = self._retry_safe(method, path, idempotency_key)
        for attempt in range(MAX_TRANSIENT_RETRIES + 1):
            headers = {
                "Accept": "application/json",
                "Authorization": f"Bearer {self.config.token}",
                "User-Agent": "helm-skill/1",
            }
            if self.config.cf_access_client_id:
                headers["CF-Access-Client-Id"] = self.config.cf_access_client_id
                headers["CF-Access-Client-Secret"] = self.config.cf_access_client_secret
            if body is not None:
                headers["Content-Type"] = "application/json"
            if if_match is not None:
                headers["If-Match"] = f'"v{if_match}"'
            if idempotency_key:
                headers["Idempotency-Key"] = idempotency_key
            req = request.Request(target, method=method, headers=headers, data=data)
            try:
                with request.build_opener(_NoRedirect).open(req, timeout=15) as response:
                    raw = response.read(MAX_RESPONSE_BYTES + 1)
                    response_headers = {key.lower(): value for key, value in response.headers.items()}
            except error.HTTPError as exc:
                response_headers = (
                    {key.lower(): value for key, value in exc.headers.items()}
                    if exc.headers is not None
                    else {}
                )
                try:
                    raw = exc.read(MAX_RESPONSE_BYTES + 1)
                finally:
                    exc.close()
                if exc.code in {429, 503} and retry_safe and attempt < MAX_TRANSIENT_RETRIES:
                    time.sleep(self._retry_delay(response_headers, attempt))
                    continue
                message, error_code = _error_details(raw)
                message = _redact_secrets(
                    message,
                    (self.config.token, self.config.cf_access_client_id, self.config.cf_access_client_secret),
                )
                raise HelmError(
                    f"Helm returned HTTP {exc.code}: {message}",
                    status_code=exc.code,
                    error_code=error_code,
                    retry_after=response_headers.get("retry-after"),
                ) from exc
            except (error.URLError, TimeoutError, OSError) as exc:
                raise HelmError(f"cannot reach Helm: {type(exc).__name__}") from exc
            if len(raw) > MAX_RESPONSE_BYTES:
                raise HelmError("Helm response exceeded the safe size limit")
            if not raw:
                return None, response_headers
            try:
                return json.loads(raw), response_headers
            except json.JSONDecodeError as exc:
                raise HelmError("Helm returned a non-JSON response") from exc


def _error_message(raw: bytes) -> str:
    return _error_details(raw)[0]


def _error_details(raw: bytes) -> tuple[str, str]:
    try:
        payload = json.loads(raw[:MAX_RESPONSE_BYTES])
    except json.JSONDecodeError:
        return "request failed", ""
    if isinstance(payload, dict):
        error_value = payload.get("error")
        if isinstance(error_value, dict):
            message = error_value.get("message")
            code = error_value.get("code")
            if isinstance(message, str):
                return message[:300], code[:120] if isinstance(code, str) else ""
    return "request failed", ""


def _redact_secrets(value: str, secrets: tuple[str, ...]) -> str:
    """Remove configured credential values from user-visible text."""

    result = value
    for secret in secrets:
        if secret:
            result = result.replace(secret, "[redacted]")
    return result[:MAX_RESPONSE_BYTES]


def _safe_error_for_client(exc: HelmError, client: Client) -> dict[str, Any]:
    payload = exc.as_dict()
    config = getattr(client, "config", None)
    secrets = (
        str(getattr(config, "token", "") or ""),
        str(getattr(config, "cf_access_client_id", "") or ""),
        str(getattr(config, "cf_access_client_secret", "") or ""),
    )
    for field in ("code", "message", "retry_after"):
        value = payload.get(field)
        if isinstance(value, str):
            payload[field] = _redact_secrets(value, secrets)[:300 if field == "message" else 120]
    return payload


