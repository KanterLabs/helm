#!/usr/bin/env python3
"""Track agent work in Helm without exposing credentials."""
from __future__ import annotations
import importlib
import sys
import types

# Legacy callers import and patch helm's top-level symbols. Responsibility
# modules bind their cross-module helpers after import; the facade mirrors
# attribute overrides so those existing interfaces remain effective.
_MODULE_NAMES = (
    "helm_config",
    "helm_http",
    "helm_collections",
    "helm_audit_model",
    "helm_audit_evidence",
    "helm_audit_review",
    "helm_audit_context",
    "helm_session_state",
    "helm_audit_submission",
    "helm_audit_reconcile",
    "helm_releases",
    "helm_focus_activity",
    "helm_focus_reader",
    "helm_focus_review",
    "helm_focus_mutations",
    "helm_notifications",
    "helm_activity",
    "helm_knowledge",
    "helm_task_lifecycle",
    "helm_progress",
    "helm_parser_projects",
    "helm_parser_focus",
    "helm_parser_activity",
    "helm_parser_review",
    "helm_parser_work",
    "helm_attachments",
    "helm_parser_attachments",
)
_modules = [importlib.import_module(name) for name in _MODULE_NAMES]
_exports = {}
for _module in _modules:
    for _name, _value in vars(_module).copy().items():
        if not _name.startswith("__") and _name != "register":
            _exports[_name] = _value
globals().update(_exports)
for _module in _modules:
    for _name, _value in _exports.items():
        _module.__dict__.setdefault(_name, _value)

class _CompatibilityModule(types.ModuleType):
    """Keep existing callers' module-level overrides effective after extraction."""
    def __setattr__(self, name, value):
        super().__setattr__(name, value)
        for module in _modules:
            if name in vars(module):
                setattr(module, name, value)

sys.modules[__name__].__class__ = _CompatibilityModule

def build_parser():
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    for name in ("helm_parser_projects", "helm_parser_focus", "helm_parser_activity", "helm_parser_review", "helm_parser_work", "helm_parser_attachments"):
        importlib.import_module(name).register(subparsers)
    return parser

def main() -> int:
    try:
        args = build_parser().parse_args()
        result = args.handler(Client(load_config()), args)
        json.dump(result, sys.stdout, ensure_ascii=False, separators=(",", ":"))
        sys.stdout.write("\n")
        return (
            1
            if args.command in {"auth-check", "auth", "auth_check", "focus"}
            and isinstance(result, dict)
            and not result.get("ok", True)
            else 0
        )
    except HelmError as exc:
        payload = exc.as_dict()
        env_secrets = tuple(
            os.environ.get(name, "")
            for name in (
                "HELM_TOKEN",
                "TC_ROADMAP_TOKEN",
                "ROADMAP_TOKEN",
                "HELM_CF_ACCESS_CLIENT_ID",
                "HELM_CF_ACCESS_CLIENT_SECRET",
                "TC_CF_ACCESS_CLIENT_ID",
                "TC_CF_ACCESS_CLIENT_SECRET",
            )
        )
        for field in ("code", "message", "retry_after"):
            value = payload.get(field)
            if isinstance(value, str):
                payload[field] = _redact_secrets(value, env_secrets)[:300 if field == "message" else 120]
        json.dump({"error": payload}, sys.stderr, ensure_ascii=False, separators=(",", ":"))
        sys.stderr.write("\n")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
