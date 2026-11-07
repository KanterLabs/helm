#!/usr/bin/env python3
"""Focus reminder isolation failures, enumerated before implementation.

Fresh sessions may have no session ID, no store, or no mapped task. Store
construction/loading can fail or reject malformed private state. All must
still produce one bounded static reminder without API calls or state writes.
Recovery must retain task/checkpoint/notes context, including when note fetch
fails. Reminder injection must not spill into tool-use/heartbeat/stop events,
leak private failures, or change the existing lifecycle decisions.
"""

from __future__ import annotations

import tempfile
import unittest
from unittest import mock

import helm_hook as hook
import helm_session as session


class FocusReminderTests(unittest.TestCase):
    def context(self, result: dict) -> str:
        output = result["hookSpecificOutput"]
        self.assertEqual(output["hookEventName"], "SessionStart")
        context = output["additionalContext"]
        self.assertEqual(context.count("Focus discovery"), 1)
        self.assertIn("explicit task scope", context)
        return context

    def test_fresh_session_without_id_or_store_needs_no_api(self) -> None:
        for event in ({"event": "SessionStart"}, {"event": "SessionStart", "source": "compact"}):
            with self.subTest(event=event), mock.patch.object(hook.helm, "Client") as client:
                context = self.context(hook.handle_event(event, store_factory=lambda _event: None))
                self.assertLess(len(context), 400)
                client.assert_not_called()

    def test_empty_private_store_is_read_only(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            store = session.StateStore("fresh", directory=directory)
            with mock.patch.object(store, "save") as save, mock.patch.object(hook.helm, "Client") as client:
                self.context(hook.handle_event({"event": "SessionStart"}, store_factory=lambda _event: store))
                save.assert_not_called()
                client.assert_not_called()

    def test_store_construction_or_malformed_state_preserves_reminder(self) -> None:
        broken = mock.Mock()
        broken.load.side_effect = session.SessionStateError("private-state-marker")
        factories = [mock.Mock(side_effect=OSError("private-directory-marker")), mock.Mock(return_value=broken)]
        for factory in factories:
            with self.subTest(factory=factory), mock.patch("sys.stderr") as stderr, mock.patch.object(hook.helm, "Client") as client:
                context = self.context(hook.handle_event({"event": "SessionStart"}, store_factory=factory))
                self.assertLess(len(context), 400)
                self.assertNotIn("private-", context)
                self.assertNotIn("private-", "".join(str(call) for call in stderr.write.call_args_list))
                client.assert_not_called()

    def test_recovery_preserves_mapping_checkpoints_and_notes(self) -> None:
        state = session.SessionState(task_id="task-1", task_key="TC-1", project_id="project-1",
                                     operation_id="op-1", agent_state="working", snapshot_ready=True,
                                     checkpoint_completed=1, checkpoint_total=3)
        store = mock.Mock()
        store.load.return_value = state
        notes = mock.Mock(return_value="Active agent notes: verified constraint.")
        context = self.context(hook.handle_event({"event": "SessionStart", "source": "compact"},
                                                store_factory=lambda _event: store, notes_fn=notes))
        self.assertIn("Recovered Helm task TC-1", context)
        self.assertIn("checkpoints 1/3", context)
        self.assertIn("verified constraint", context)
        notes.assert_called_once_with(state)

    def test_other_events_without_state_remain_empty(self) -> None:
        for name in ("PostToolUse", "PreCompact", "Stop", "Unknown"):
            with self.subTest(name=name), mock.patch.object(hook.helm, "Client") as client:
                self.assertEqual(hook.handle_event({"event": name}, store_factory=lambda _event: None), {})
                client.assert_not_called()


if __name__ == "__main__":
    unittest.main(verbosity=2)
