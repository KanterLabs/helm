"""Failure-first Focus CLI HTTP journeys, using synthetic content only.

Recorded before implementation: missing/ambiguous project or Focus must not
write; cross-project tasks must not leak scope; current strong ETags must be
used and explicit stale versions must surface conflicts without retry; omitted
metadata must survive while explicit null clears; frozen and nonempty release
errors must remain visible; membership must never claim/move tasks; duplicate
task arguments must not cause duplicate writes; partial batches must stop and
preserve a stable operation identity with a safe resume list; empty reason,
invalid dates and invalid versions must fail before writes; legacy release
aliases and agent-claim release must retain their parser contracts. Lost write
responses must preserve the operation ID for reconciliation, never fabricate
success. Sanitized JSON CLI captures are repeatable artifacts.
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

SCRIPT = Path(__file__).with_name("helm.py")
OPERATION = "a0000000-0000-4000-8000-000000000001"


class FocusFixture(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def reply(self, status, body=None, *, version=None):
        raw = b"" if body is None else json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        if version:
            self.send_header("ETag", f'"v{version}"')
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        state = self.server.state
        path = urlsplit(self.path).path.removeprefix("/api/v1")
        state["gets"].append(self.path)
        if path == "/projects":
            return self.reply(200, {"data": state["projects"], "next_cursor": ""})
        if path == "/projects/project-1/releases":
            return self.reply(200, {"data": list(state["focuses"].values()), "next_cursor": ""})
        if path.startswith("/releases/"):
            focus = state["focuses"].get(path.rsplit("/", 1)[-1])
            if focus:
                return self.reply(200, focus, version=focus["version"] if state["etag"] else None)
        if path.startswith("/tasks/"):
            ref = path.rsplit("/", 1)[-1]
            task = next((t for t in state["tasks"].values() if ref in (t["id"], t["key"])), None)
            if task:
                return self.reply(200, task, version=task["version"] if state["etag"] else None)
        return self.reply(404, {"error": {"code": "not_found", "message": "synthetic missing"}})

    def do_POST(self):
        self.mutate()

    def do_PATCH(self):
        self.mutate()

    def do_DELETE(self):
        self.mutate()

    def mutate(self):
        state = self.server.state
        path = urlsplit(self.path).path.removeprefix("/api/v1")
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")
        key = self.headers.get("Idempotency-Key", "")
        state["writes"].append({"method": self.command, "path": path, "body": body,
                                "etag": self.headers.get("If-Match"), "operation": key})
        if state.get("malformed_write_response"):
            return self.reply(200, {"unexpected": True})
        if state.get("lose_write_response"):
            state["lose_write_response"] = False
            self.close_connection = True
            return
        if path == "/projects/project-1/releases":
            focus = {"id": "focus-new", "project_id": "project-1", "name": body["name"],
                     "description": "", "target_date": None, "status": "planned", "version": 1, **body}
            state["focuses"][focus["id"]] = focus
            return self.reply(201, focus, version=1)
        if path.startswith("/tasks/"):
            task = state["tasks"][path.rsplit("/", 1)[-1]]
            if state["fail_task"] == task["id"]:
                return self.reply(409, {"error": {"code": "stale_task", "message": "synthetic task changed"}})
            if self.headers.get("If-Match") != f'"v{task["version"]}"':
                return self.reply(409, {"error": {"code": "stale_task", "message": "synthetic stale"}})
            focus = state["focuses"].get(body.get("release_id") or task.get("release_id"))
            if focus and focus["status"] == "released":
                return self.reply(409, {"error": {"code": "release_frozen", "message": "synthetic frozen"}})
            task.update(body)
            task["version"] += 1
            return self.reply(200, task, version=task["version"])
        parts = path.split("/")
        focus = state["focuses"].get(parts[2])
        if self.headers.get("If-Match") != f'"v{focus["version"]}"':
            return self.reply(409, {"error": {"code": "conflict", "message": "synthetic stale"}})
        if len(parts) == 4 and parts[3] == "reopen":
            focus["status"] = "planned"
        elif focus["status"] == "released":
            return self.reply(409, {"error": {"code": "release_frozen", "message": "synthetic frozen"}})
        elif self.command == "DELETE":
            if any(t.get("release_id") == focus["id"] for t in state["tasks"].values()):
                return self.reply(409, {"error": {"code": "release_has_tasks", "message": "synthetic nonempty"}})
            del state["focuses"][focus["id"]]
            return self.reply(204)
        elif len(parts) == 4 and parts[3] == "complete":
            focus["status"] = "released"
        else:
            focus.update(body)
        focus["version"] += 1
        return self.reply(200, focus, version=focus["version"])


class FocusMutationJourneys(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), FocusFixture)
        self.server.state = {
            "projects": [{"id": "project-1", "key": "SYN", "name": "Synthetic"}],
            "focuses": {"focus-1": {"id": "focus-1", "project_id": "project-1", "name": "Onboarding",
                                      "description": "Preserve scope", "target_date": "2026-11-30",
                                      "version": 3, "status": "planned"}},
            "tasks": {f"task-{n}": {"id": f"task-{n}", "key": f"SYN-{n}", "project_id": "project-1",
                                      "column_id": "backlog", "claimed_by": None,
                                      "release_id": None, "version": n} for n in range(1, 4)},
            "gets": [], "writes": [], "fail_task": "", "etag": True,
        }
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.temp.cleanup()

    def cli(self, *args, code=0):
        env = {**os.environ, "HELM_URL": f"http://127.0.0.1:{self.server.server_port}",
               "HELM_TOKEN": "synthetic-token", "HELM_CONFIG": str(Path(self.temp.name) / "missing")}
        for key in ("TC_ROADMAP_URL", "TC_ROADMAP_TOKEN", "ROADMAP_TOKEN", "TC_ROADMAP_CONFIG"):
            env.pop(key, None)
        run = subprocess.run([sys.executable, str(SCRIPT), *args], env=env, text=True, capture_output=True)
        captured = {"args": list(args), "returncode": run.returncode, "stdout": run.stdout, "stderr": run.stderr}
        if artifact := os.environ.get("HELM_FOCUS_TEST_ARTIFACT"):
            with open(artifact, "a", encoding="utf-8") as out:
                out.write(json.dumps(captured) + "\n")
        self.assertEqual(run.returncode, code, captured)
        self.assertNotIn("synthetic-token", run.stdout + run.stderr)
        return json.loads(run.stdout or run.stderr) if code != 2 else run.stderr

    def focus(self, action, *args, code=0):
        return self.cli("focus", action, "--project", "SYN", "--focus", "onboarding", *args, code=code)

    def test_metadata_lifecycle_and_explicit_clearing(self):
        created = self.cli("focus", "create", "--project", "SYN", "--name", "Import", "--operation-id", OPERATION)
        self.assertEqual(created["focus"]["name"], "Import")
        edited = self.focus("edit", "--name", "Welcome")
        self.assertEqual(edited["focus"]["description"], "Preserve scope")
        self.assertEqual(self.server.state["writes"][-1]["etag"], '"v3"')
        cleared = self.cli("focus", "edit", "--project", "SYN", "--focus", "focus-1",
                           "--clear-description", "--clear-target-date")
        self.assertIsNone(cleared["focus"]["description"])
        self.assertIsNone(cleared["focus"]["target_date"])
        self.assertEqual(self.server.state["writes"][-1]["body"], {"description": None, "target_date": None})

    def test_membership_is_scoped_deduplicated_and_does_not_claim_or_move(self):
        result = self.focus("assign", "--task", "SYN-1", "--task", "SYN-1", "--task", "SYN-2", "--operation-id", OPERATION)
        self.assertTrue(result["ok"])
        self.assertEqual(len(result["succeeded"]), 2)
        writes = self.server.state["writes"]
        self.assertEqual([w["path"] for w in writes], ["/tasks/task-1", "/tasks/task-2"])
        self.assertEqual(len({w["operation"] for w in writes}), 2)
        for task in self.server.state["tasks"].values():
            self.assertIsNone(task["claimed_by"])
            self.assertEqual(task["column_id"], "backlog")
        self.assertEqual([w["body"] for w in writes], [{"release_id": "focus-1"}] * 2)
        self.focus("unassign", "--task", "SYN-1")
        self.assertIsNone(self.server.state["tasks"]["task-1"]["release_id"])

    def test_partial_membership_has_stable_resume_and_stops_after_error(self):
        self.server.state["fail_task"] = "task-2"
        result = self.focus("assign", "--task", "SYN-1", "--task", "SYN-2", "--task", "SYN-3", "--operation-id", OPERATION, code=1)
        self.assertFalse(result["ok"])
        self.assertEqual(result["operation_id"], OPERATION)
        self.assertEqual(result["failed"]["error"]["code"], "stale_task")
        self.assertEqual(result["pending"], ["SYN-3"])
        self.assertEqual(result["resume"]["tasks"], ["SYN-2", "SYN-3"])
        failed_key = self.server.state["writes"][-1]["operation"]
        self.server.state["fail_task"] = ""
        self.focus("assign", "--task", "SYN-2", "--task", "SYN-3", "--operation-id", OPERATION)
        self.assertEqual(self.server.state["writes"][-2]["operation"], failed_key)

    def test_malformed_success_cannot_fabricate_mutation_success(self):
        self.server.state["malformed_write_response"] = True
        result = self.cli("focus", "create", "--project", "SYN", "--name", "Import", code=1)
        self.assertFalse(result["ok"])
        self.assertEqual(result["operation_id"], self.server.state["writes"][-1]["operation"])
        self.assertIn("reconcile", result["next_action"])

    def test_lost_response_retains_identity_for_reconciliation(self):
        self.server.state["lose_write_response"] = True
        result = self.cli("focus", "create", "--project", "SYN", "--name", "Import", code=1)
        self.assertFalse(result["ok"])
        self.assertEqual(result["operation_id"], self.server.state["writes"][-1]["operation"])
        self.assertIn("reconcile", result["next_action"])
        self.assertEqual(len(self.server.state["writes"]), 1)

    def test_unassign_refuses_to_clear_an_unrelated_focus(self):
        self.server.state["tasks"]["task-1"]["release_id"] = "foreign-focus"
        result = self.focus("unassign", "--task", "SYN-1", code=1)
        self.assertFalse(result["ok"])
        self.assertEqual(self.server.state["writes"], [])

    def test_scope_guards_and_missing_strong_etag_prevent_writes(self):
        self.server.state["tasks"]["task-1"]["project_id"] = "foreign"
        result = self.focus("assign", "--task", "SYN-1", code=1)
        self.assertFalse(result["ok"])
        self.assertEqual(self.server.state["writes"], [])
        self.server.state["etag"] = False
        self.focus("edit", "--description", "New", code=1)
        self.assertEqual(self.server.state["writes"], [])

    def test_conflicts_frozen_and_empty_delete_are_explicit(self):
        stale = self.focus("edit", "--description", "Concurrent", "--version", "1", "--operation-id", OPERATION, code=1)
        self.assertEqual(stale["error"]["code"], "conflict")
        self.assertEqual(len(self.server.state["writes"]), 1)
        self.focus("assign", "--task", "SYN-1")
        nonempty = self.focus("delete", code=1)
        self.assertEqual(nonempty["error"]["code"], "release_has_tasks")
        self.focus("complete")
        frozen = self.focus("edit", "--name", "Frozen", code=1)
        self.assertEqual(frozen["error"]["code"], "release_frozen")
        self.focus("reopen", "--reason", "More onboarding work")
        self.assertEqual(self.server.state["writes"][-1]["body"], {"reason": "More onboarding work"})
        self.focus("unassign", "--task", "SYN-1")
        result = self.focus("delete")
        self.assertTrue(result["deleted"])

    def test_invalid_inputs_and_ambiguous_names_cannot_write(self):
        self.focus("edit", code=1)
        self.focus("reopen", "--reason", " ", code=1)
        self.focus("edit", "--target-date", "2026-02-30", code=1)
        self.focus("edit", "--description", "x", "--version", "0", code=2)
        self.focus("assign", "--task", "SYN-1", "--task", "SYN-2", "--version", "1", code=1)
        self.server.state["projects"].append({"id": "project-2", "key": "SYN", "name": "Other"})
        self.focus("edit", "--description", "x", code=1)
        self.assertEqual(self.server.state["writes"], [])

    def test_read_aliases_and_review_parser_keep_project_scope(self):
        listed = self.cli("focus", "list", "--project", "SYN", "--status", "planned")
        self.assertEqual(listed["releases"][0]["id"], "focus-1")
        self.assertEqual(self.focus("get")["id"], "focus-1")
        self.assertEqual(self.cli("releases", "get", "--project", "SYN", "--release", "focus-1")["id"], "focus-1")
        self.cli("focus", "review", "--project", "SYN", "--inactive-days", "0", code=2)
        self.cli("focus", "get", "--focus", "focus-1", code=2)
        self.assertEqual(self.server.state["writes"], [])


if __name__ == "__main__":
    unittest.main()
