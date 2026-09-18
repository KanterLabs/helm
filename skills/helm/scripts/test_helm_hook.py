#!/usr/bin/env python3
"""Focused tests for the Helm lifecycle hook adapter."""

from __future__ import annotations

import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest import mock

import helm_session as session
import helm_hook as hook


def _state(**changes: object) -> session.SessionState:
    values: dict[str, object] = {
        "task_id": "task-1",
        "task_key": "TC-1",
        "project_id": "project-1",
        "operation_id": "session/op-1",
        "agent_state": "working",
        "snapshot_ready": True,
    }
    values.update(changes)
    return session.SessionState(**values)  # type: ignore[arg-type]


class HookTests(unittest.TestCase):
    def make_store(self, root: str, state: session.SessionState | None = None) -> session.StateStore:
        store = session.StateStore("session-1", directory=root)
        if state is not None:
            store.save(state)
        return store

    def test_parser_accepts_event_aliases_and_rejects_non_objects(self) -> None:
        self.assertEqual(hook.parse_event('{"event":"SessionStart","session_id":"s"}')['event'], "SessionStart")
        self.assertEqual(hook._event_name({"event_name": "post_tool_use"}), "PostToolUse")
        with self.assertRaises(ValueError):
            hook.parse_event("[]")

    def test_compact_session_start_injects_safe_recovery_context(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            store = self.make_store(raw, _state(checkpoint_completed=2, checkpoint_total=4))
            event = {"hook_event_name": "SessionStart", "source": "compact", "session_id": "session-1"}
            result = hook.handle_event(
                event,
                store_factory=lambda _event: store,
                notes_fn=lambda _state: "Active agent notes (review before continuing):\n- [Known issue] Do not retry the rejected path.",
            )
            context = result["hookSpecificOutput"]["additionalContext"]
            self.assertIn("Recovered Helm task TC-1", context)
            self.assertIn("agent state working", context)
            self.assertIn("checkpoints 2/4", context)
            self.assertIn("operation session/op-1", context)
            self.assertNotIn("session-1", context)
            self.assertNotIn("summary", context)
            self.assertIn("Do not retry the rejected path", context)

    def test_session_start_warns_when_notes_cannot_be_refreshed(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            store = self.make_store(raw, _state())
            with mock.patch("sys.stderr"):
                result = hook.handle_event(
                    {"event": "SessionStart", "session_id": "session-1"},
                    store_factory=lambda _event: store,
                    notes_fn=lambda _state: (_ for _ in ()).throw(RuntimeError("private failure")),
                )
            context = result["hookSpecificOutput"]["additionalContext"]
            self.assertIn("Agent notes could not be refreshed", context)
            self.assertNotIn("private failure", context)

    def test_initial_snapshot_reminder_and_no_heartbeat_before_progress(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            store = self.make_store(raw, _state(snapshot_ready=False))
            heartbeat = mock.Mock()
            event = {"hook_event_name": "PostToolUse", "session_id": "session-1"}
            result = hook.handle_event(event, store_factory=lambda _event: store, heartbeat_fn=heartbeat)
            self.assertIn("hookSpecificOutput", result)
            self.assertIn("additionalContext", result["hookSpecificOutput"])
            self.assertIn("initial structured progress", result["hookSpecificOutput"]["additionalContext"])
            heartbeat.assert_not_called()

    def test_heartbeat_is_throttled_and_uses_only_safe_call(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            start = datetime(2026, 1, 1, tzinfo=timezone.utc)
            store = self.make_store(raw, _state(last_progress_at="2025-12-31T23:50:00Z"))
            sent: list[session.StateStore] = []
            heartbeat = lambda value: sent.append(value)  # noqa: E731
            with mock.patch.dict("os.environ", {"HELM_HEARTBEAT_INTERVAL_SECONDS": "480"}, clear=False):
                result = hook.handle_event({"event": "PostToolUse", "session_id": "session-1"}, store_factory=lambda _event: store, now=start, heartbeat_fn=heartbeat)
            self.assertEqual(result, {})
            self.assertEqual(sent, [store])

            # A heartbeat callback that does not touch state is still a valid
            # injected test double; the store itself enforces the throttle.
            store.update(last_heartbeat_at="2026-01-01T00:01:00Z")
            sent.clear()
            with mock.patch.dict("os.environ", {"HELM_HEARTBEAT_INTERVAL_SECONDS": "480"}, clear=False):
                hook.handle_event({"event": "PostToolUse", "session_id": "session-1"}, store_factory=lambda _event: store, now=start + timedelta(minutes=2), heartbeat_fn=heartbeat)
            self.assertEqual(sent, [])

    def test_network_failure_fails_open(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            store = self.make_store(raw, _state(last_progress_at="2025-12-31T23:00:00Z"))
            with mock.patch.object(hook, "_heartbeat", side_effect=RuntimeError("network secret")):
                with mock.patch("sys.stderr") as stderr:
                    result = hook.handle_event({"event": "PreCompact", "session_id": "session-1"}, store_factory=lambda _event: store, now=datetime(2026, 1, 1, tzinfo=timezone.utc))
            self.assertEqual(result, {})
            self.assertNotIn("secret", "".join(str(call) for call in stderr.write.call_args_list))

    def test_stop_blocks_once_then_stop_hook_guard_allows_continuation(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            store = self.make_store(raw, _state(agent_state="verifying"))
            event = {"event": "Stop", "session_id": "session-1"}
            first = hook.handle_event(event, store_factory=lambda _event: store, reconcile_fn=lambda _store, _state: False)
            self.assertEqual(first["decision"], "block")
            self.assertIn("TC-1", first["reason"])
            continuation = hook.handle_event({**event, "stop_hook_active": True}, store_factory=lambda _event: store, reconcile_fn=lambda _store, _state: False)
            self.assertEqual(continuation, {})

    def test_waiting_handoff_missing_id_and_unknown_event_allow_stop(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            store = self.make_store(raw, _state(agent_state="waiting"))
            self.assertEqual(hook.handle_event({"event": "Stop", "session_id": "session-1"}, store_factory=lambda _event: store), {})
            self.assertEqual(hook.handle_event({"event": "Stop"}), {})
            self.assertEqual(hook.handle_event({"event": "Unknown", "session_id": "session-1"}, store_factory=lambda _event: store), {})

    def test_stop_reconciles_server_terminal_task_and_clears_stale_state(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            store = self.make_store(raw, _state(agent_state="verifying"))
            reconcile = mock.Mock(return_value=True)
            result = hook.handle_event(
                {"event": "Stop", "session_id": "session-1"},
                store_factory=lambda _event: store,
                reconcile_fn=reconcile,
            )
            self.assertEqual(result, {})
            reconcile.assert_called_once_with(store, _state(agent_state="verifying"))

    def test_stop_does_not_reconcile_or_clear_when_server_task_is_still_active(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            store = self.make_store(raw, _state(agent_state="working"))
            reconcile = mock.Mock(return_value=False)
            result = hook.handle_event(
                {"event": "Stop", "session_id": "session-1"},
                store_factory=lambda _event: store,
                reconcile_fn=reconcile,
            )
            self.assertEqual(result["decision"], "block")

    def test_terminal_reconciliation_uses_server_lifecycle_and_unclaimed_status(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            for semantic in ("completed", "blocked"):
                store = self.make_store(raw + semantic, _state(agent_state="verifying"))
                client = mock.Mock()
                client.call.return_value = ({"semantic_state": semantic, "claimed_by": None}, {})
                with mock.patch.object(hook.helm, "load_config"), mock.patch.object(hook.helm, "Client", return_value=client):
                    self.assertTrue(hook._reconcile_terminal_state(store, _state(agent_state="verifying")))
                self.assertIsNone(store.load())

    def test_terminal_reconciliation_keeps_claimed_task_state(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            store = self.make_store(raw, _state(agent_state="working"))
            client = mock.Mock()
            client.call.return_value = ({"semantic_state": "completed", "claimed_by": "agent-2"}, {})
            with mock.patch.object(hook.helm, "load_config"), mock.patch.object(hook.helm, "Client", return_value=client):
                self.assertFalse(hook._reconcile_terminal_state(store, _state(agent_state="working")))
            self.assertIsNotNone(store.load())


if __name__ == "__main__":
    unittest.main(verbosity=2)
