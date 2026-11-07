"""Explicit Focus lifecycle and task membership writes with guarded retries.

Cross-module helpers are injected by helm's compatibility facade. These writes
never claim tasks, change their columns, or complete a Focus implicitly.
"""
from __future__ import annotations

import re
from datetime import datetime
from typing import Any
from urllib import parse


def _focus_metadata(args, *, create=False):
    body = {}
    name = getattr(args, "name", None)
    if name is not None:
        if not str(name).strip():
            raise HelmError("Focus name must not be empty")
        body["name"] = str(name).strip()
    if create and "name" not in body:
        raise HelmError("Focus name is required")
    description = getattr(args, "description", None)
    if description is not None:
        if len(description) > 10000:
            raise HelmError("Focus description must be at most 10000 characters")
        body["description"] = description
    if getattr(args, "clear_description", False):
        body["description"] = None
    target = getattr(args, "target_date", None)
    if target is not None:
        if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", target):
            raise HelmError("target date must be YYYY-MM-DD")
        try:
            datetime.strptime(target, "%Y-%m-%d")
        except ValueError as exc:
            raise HelmError("target date must be a valid YYYY-MM-DD date") from exc
        body["target_date"] = target
    if getattr(args, "clear_target_date", False):
        body["target_date"] = None
    if not body:
        raise HelmError("Focus edit requires a metadata field or an explicit clear option")
    return body


def _focus_expected_version(payload, headers, override=None):
    if override is not None:
        if isinstance(override, bool) or not isinstance(override, int) or override < 1:
            raise HelmError("version must be a positive integer")
        return override
    version = payload.get("version")
    if isinstance(version, bool) or not isinstance(version, int) or version < 1:
        raise HelmError("Helm returned an invalid version")
    if headers.get("etag") != f'"v{version}"':
        raise HelmError("Helm did not return a matching strong ETag; re-read before writing")
    return version


def _focus_current(client, args):
    project, resolved = _release_reference(client, str(args.project), str(args.release))
    path = "/releases/" + parse.quote(resolved["id"], safe="")
    focus, headers = client.call("GET", path)
    if (not isinstance(focus, dict) or focus.get("id") != resolved["id"]
            or focus.get("project_id") != project["id"]):
        raise HelmError("Focus does not belong to the requested project or returned invalid identity")
    return project, focus, headers, path


def _focus_mutation_record(payload, expected_id=None):
    if (not isinstance(payload, dict) or not isinstance(payload.get("id"), str)
            or not payload["id"].strip() or isinstance(payload.get("version"), bool)
            or not isinstance(payload.get("version"), int) or payload["version"] < 1
            or (expected_id is not None and payload["id"] != expected_id)):
        raise HelmError("Helm returned an invalid mutation response; reconcile before retrying")


def _focus_write(client, method, path, *, operation_id, body=None, version=None):
    """Keep failed write identity visible even when its response was lost.

    The HTTP transport may retry transient responses using this same key. This
    layer never retries conflicts, refreshes a stale version, or changes intent.
    """
    try:
        result, _ = client.call(method, path, body=body, if_match=version,
                                idempotency_key=_new_mutation_idempotency(operation_id))
        if method != "DELETE":
            _focus_mutation_record(result)
    except HelmError as exc:
        return {"ok": False, "operation_id": operation_id, "error": exc.as_dict(),
                "next_action": "Re-read and reconcile the result before retrying the same logical operation with this operation_id."}
    return {"ok": True, "operation_id": operation_id, "focus": result}


def cmd_focus_create(client, args):
    body = _focus_metadata(args, create=True)
    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    project = _release_project(client, str(args.project))
    path = "/projects/" + parse.quote(project["id"], safe="") + "/releases"
    result = _focus_write(client, "POST", path, operation_id=operation_id, body=body)
    result["project"] = project.get("key")
    return result


def _focus_change(client, args, action, *, body=None):
    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    project, focus, headers, path = _focus_current(client, args)
    version = _focus_expected_version(focus, headers, getattr(args, "version", None))
    method = "PATCH" if action == "edit" else "DELETE" if action == "delete" else "POST"
    if action in {"complete", "reopen"}:
        path += "/" + action
    result = _focus_write(client, method, path, operation_id=operation_id, body=body, version=version)
    result.update(project=project.get("key"), focus_id=focus["id"])
    if action == "delete" and result["ok"]:
        result.pop("focus", None)
        result["deleted"] = True
    return result


def cmd_focus_edit(client, args):
    return _focus_change(client, args, "edit", body=_focus_metadata(args))


def cmd_focus_complete(client, args):
    return _focus_change(client, args, "complete", body={})


def cmd_focus_reopen(client, args):
    reason = str(getattr(args, "reason", "") or "").strip()
    if not reason:
        raise HelmError("Reopening a Focus requires a non-empty reason")
    return _focus_change(client, args, "reopen", body={"reason": reason})


def cmd_focus_delete(client, args):
    return _focus_change(client, args, "delete")


def _focus_membership(client, args, *, assigning):
    references = list(dict.fromkeys(str(ref).strip() for ref in (getattr(args, "task", None) or [])))
    if not references or any(not ref for ref in references):
        raise HelmError("At least one non-empty task reference is required")
    override = getattr(args, "version", None)
    if override is not None and len(references) != 1:
        raise HelmError("--version applies to a single task; omit it for a multi-task membership operation")
    operation_id = _new_operation_id(getattr(args, "operation_id", None))
    project, focus, _headers, _path = _focus_current(client, args)
    desired = focus["id"] if assigning else None
    body = {"release_id": desired}
    succeeded = []
    seen_ids = set()
    result = {"ok": True, "project": project.get("key"), "focus_id": focus["id"],
              "operation_id": operation_id, "atomic": False, "succeeded": succeeded,
              "failed": None, "pending": []}
    for index, reference in enumerate(references):
        try:
            task, headers = client.call("GET", "/tasks/" + parse.quote(reference, safe=""))
            if (not isinstance(task, dict) or task.get("project_id") != project["id"]
                    or not isinstance(task.get("id"), str) or not task["id"].strip()):
                raise HelmError("Task does not belong to the requested project or returned invalid identity")
            task_id = task["id"]
            if task_id in seen_ids:
                continue
            seen_ids.add(task_id)
            current = task.get("release_id")
            if not assigning and current not in (None, focus["id"]):
                raise HelmError("Task belongs to a different Focus; re-read membership before unassigning")
            # A resume may include an earlier successful task. Report current
            # membership without issuing another write or replaying old state.
            if current == desired and override is None:
                succeeded.append({"task": task.get("key", reference), "id": task_id,
                                  "version": task.get("version"), "changed": False})
                continue
            version = _focus_expected_version(task, headers, override)
            path = "/tasks/" + parse.quote(task_id, safe="")
            # Version must not be part of the seed: a resumed batch reuses the
            # same logical per-task mutation identity after a fresh GET.
            key = _derived_uuid4("\0".join((operation_id, "PATCH", path, str(desired))))
            changed, _ = client.call("PATCH", path, body=body, if_match=version, idempotency_key=key)
            _focus_mutation_record(changed, task_id)
            succeeded.append({"task": task.get("key", reference), "id": task_id,
                              "version": changed.get("version"), "changed": True})
        except HelmError as exc:
            result.update(ok=False, failed={"task": reference, "error": exc.as_dict()}, pending=references[index + 1:])
            result["resume"] = {
                "project": project["id"], "focus": focus["id"],
                "action": "assign" if assigning else "unassign", "operation_id": operation_id,
                "tasks": references[index:],
                "next_action": "Re-read failed/current memberships and resolve the error; then resume only remaining authorized tasks using the same operation_id. No completed task is rolled back.",
            }
            break
    return result


def cmd_focus_assign(client, args):
    return _focus_membership(client, args, assigning=True)


def cmd_focus_unassign(client, args):
    return _focus_membership(client, args, assigning=False)
