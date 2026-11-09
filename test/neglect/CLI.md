# Neglect signal CLI adoption: failure modes

TC-381, 8 November 2026, for plan D7 in
[the neglect signal plan](../../docs/FOCUS_NEGLECT_SIGNAL_PLAN.md). Written
before the CLI change. Owners: real-flow cases 18 and 19 (`cases_cli.py`),
20 (`cases_cli_fallback.py`), guard case 17, and the isolation tests in
`skills/helm/scripts/test_helm_focus_signal.py`.

## Failure modes

1. **Older server output drifts.** Against a server without the signal any
   byte of `focus review` output differs from the version 1 CLI (main
   `1c54493`): an extra probe request changes `coverage.requests`, a new
   key appears, or the continuation cursor bytes change. (Case 20 compares
   both CLIs on the real `1c54493` binary in six modes; case 17.)
2. **Rollback binary is the candidate.** Cases 17 and 20 silently run
   against a server that has the signal and prove nothing. (Both cases fail
   when the retained binary returns `Task.activity`.)
3. **Server path still replays timelines.** `histories_read > 0`, a
   `timeline_next`, or request count growing with two reads per card.
   (Case 18.)
4. **Path not reported.** Output does not say whether the server signal or
   version 1 classification produced it. (Case 18; absence of
   `activity_signal` is the version 1 indicator, case 20.)
5. **Bounded misread.** A `bounded` card inside the window is assessed
   `active`, gets an `inactive` finding, or is dropped; a `bounded` card at
   or past the threshold is not `inactive`. (Case 18; isolation.)
6. **Unknown dropped.** An `unknown` card is missing from the review or has
   no `activity_unknown` finding. (Case 18.)
7. **Pending or incomplete coverage hidden.** A `pending` card is assessed
   active or inactive, or incomplete `activity_coverage` leaves
   `coverage.partial` false, so the result reads as "nothing neglected".
   (Case 19; isolation.)
8. **Threshold not mapped.** `--inactive-days` is not sent as the server's
   `inactive_days`, or the client judges with its own clock instead of the
   server's `evaluated_at`. (Case 18 runs 10 and 14 days; isolation boundary
   test at the exact instant and one millisecond after.)
9. **Budget flags silently ignored.** `--timeline-limit` has no effect on
   the server path and is not reported; `--history-limit`,
   `--request-limit` and `--time-limit` stop bounding the review. (Case 18.)
10. **Focus evidence lost.** Dispositions, blockers and owned work from the
    Focus work queue no longer produce findings on the server path. (Case 18.)
11. **Continuation breaks.** A server-path cursor skips or repeats cards,
    or a continuation forgets the server path. (Case 18 `--history-limit 1`
    chain; isolation cursor round trip.)
12. **Out-of-range threshold crashes.** `--inactive-days` above the server
    maximum (3650) fails against a new server instead of using version 1.
    (Isolation.)
13. **Review writes.** The server path creates events. (Case 18 event count.)
14. **Installed skill differs.** Only the checkout is tested. (TC-381
    verification installs the skill under `$TMPDIR` and runs it.)
