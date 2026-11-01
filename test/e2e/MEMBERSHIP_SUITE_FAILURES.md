# Parent final-evidence gate failure inventory

Reports and copied SQLite/log evidence can be written after browser hooks.
Retain only one-way credential fingerprints in private fixture state, scan
after all writers stop, remove contaminated evidence before hashing/upload,
and fail the run. Similar unregistered text must not be removed. Malformed
fingerprints must fail closed rather than certify unchecked evidence. Ordinary
focused runs with no membership fingerprints keep their existing behavior.

# Local membership phase: failure inventory

Recorded before changing `run-suites.py` or its proofs. The main browser
phase uses disabled auth, so genuine TC-113 member journeys need their own
fresh local-auth fixture.

- Full runs never execute the membership journeys: the spec self-skips under
  disabled auth and no local phase selects it, so CI reports green without
  member evidence. A full run must require a `membership` phase.
- Unrelated focused invocations unexpectedly acquire the membership fixture,
  adding time and a second local server. Focused runs schedule it only when
  the exact `e2e/membership-sessions.spec.ts` operand is selected.
- Membership tests leak into onboarding (shared fresh database consumed by the
  wrong first-run assertion) or onboarding leaks into membership. Each phase
  selects exactly one spec file in its own state directory.
- The membership fixture reuses another fixture's ports. It uses the unused
  upper half of the onboarding block (+50..+80), so the existing run.sh range
  and every other fixture's block are unchanged.
- The new phase escapes the global worker budget, starts before main finishes,
  is not persisted in `suites.json`, or its failure is hidden. It follows the
  same launch/collect/required-phase rules as other ancillary phases.
- The inherited stalled-account flag poisons the fixture. It is stripped as for
  every ordinary phase.

Proofs: `python3 test/e2e/test_membership_suite.py` uses the synthetic suite
harness from `test_run_suites.py`; no browsers, builds or servers.
