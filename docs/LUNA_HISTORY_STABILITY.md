# Luna history stability

The run-history detail cache must merge concurrent responses against the latest
map. A user can expand two persisted runs before either detail request returns.
The first response must remain visible while the second is delayed, and both
responses must remain visible after the second response arrives.

The regression workflow creates two runs through the real task-draft API and
the deterministic Codex app-server fixture. It loads the real run list from
Helm, delays only the two detail responses after `route.fetch()`, expands both
rows, releases the first response, checks its persisted input and output, then
releases the second response and checks both rows again. Evidence includes a
sanitized synthetic response summary and a Settings screenshot.

The failure signature is that the later detail response replaces the map entry
written by the earlier response. The first expanded row then remains on
“Loading input and output…” until the user collapses and reopens it.
