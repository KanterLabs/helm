# TC-308 failure inventory (before implementation)

The evaluator is a new, explicit v2 path. Legacy external submission and older
captures retain v1 semantics; malformed v2 must never fall back to v1.

- Scope: missing, duplicated, foreign or changed task/condition identities;
  rewritten quotes/origins; missing required conditions; zero-condition Done.
- Proof: invented IDs/paths/refs; cross-card or cross-condition citations;
  README promises/test source mistaken for implementation; PR-only code or
  checks from a different commit mistaken for baseline implementation/checks.
- Placement: unrelated unread files poison supported conditions; unknown means
  Done; no evidence means unstarted; failed CI means Blocked; demonstrated partial
  Done cannot reopen; correct Active becomes a move; ambiguous actual columns
  resolve by arbitrary order; foreign claims change the recommendation.
- Actual-run regression: an unknown condition plus a cited `work_remaining`
  assertion falsely confirms Active or reopens Done. Unknown evidence must stay
  unverifiable; Active needs baseline implementation and a contradicted required
  condition. A known contradiction may coexist with other unknown conditions.
- Context: checklist ticks/prose become code proof; actual columns, claims,
  dependency/checklist policy or concrete blocker facts disappear from capture.
- Discovery: ordinary English without literal anchors never produces excerpts;
  linked-PR hints silently change baseline; model paths bypass tree membership;
  repeated planning consumes unbounded calls/files/bytes/time or starves cards;
  cancellation/revocation between planning and reads/publication is ignored.
- Persistence: partial writes survive invalid scope; old history is rewritten;
  populated migration loses relationships; old SQL/binary cannot read/write;
  portable export drops additive assessments; application eligibility becomes
  model permission or bypasses the later lifecycle implementation.

Tests are written before corresponding implementation. Focused real HTTP/provider,
store/worker and populated migration scenarios save synthetic response receipts.
No production data, provider tokens or private task content belongs in artifacts.
