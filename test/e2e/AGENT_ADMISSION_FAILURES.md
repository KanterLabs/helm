# Agent admission failure inventory

The synthetic acceptance tests exercise the HTTP API on temporary loopback
servers. The upgrade scenario uses the same isolated SQLite database with the
pre-change and candidate binaries. No network account or fixture token is saved.

| Failure | Evidence in `result.json` |
| --- | --- |
| Two agents race for the fifth slot and both win, or valid work is denied | Concurrent responses resolve to one admission and one capacity conflict, with Active increasing from four to five. |
| A denial leaves a task, work snapshot, comment, or event behind | Exactly one new task appears in the race. A subsequent full-capacity denial preserves project task rows and associated event, comment, checklist, and work counts. |
| An idempotent retry creates another task, republishes events, or returns inconsistent admission data | The winning admission replays the same data and the database counts remain unchanged. |
| An Active claim in one project allows the same agent to start work in another | The agent gets a conflict in the second project. A credential scoped to that project sees no private details from the first project. |
| Recovering a stale Active card consumes a sixth slot or leaves the prior session in control | Recovery admits the original card at five of five, creates no new task and fences subsequent renewals from the prior session. |
| A pre-change project loses or rewrites data when the new server starts over capacity | Six existing Active cards remain visible, and task, claim, comment, progress, checklist and event rows match across the restart. |
| Capacity blocks reordering or exit, or a normal human board operation bypasses the cap | A same-column reorder succeeds at six. Reorders into Active fail above and at capacity. Creating or updating into Active also fails at capacity. Normal transitions reopen a slot. |

```bash
python3 test/e2e/agent-admission.py \
  --baseline-binary /path/to/pre-change/helm \
  --binary /path/to/candidate/helm \
  --output .artifacts/agent-admission
```

Build the binaries separately from the corresponding revisions. Use a fresh
output directory. The test writes only sanitized assertions, counters,
response statuses and codes, and binary checksums to `result.json`.
