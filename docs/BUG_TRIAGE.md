# Bug intake and ticket triage

## Failure cases identified before implementation

- Adding Bugs must not move, renumber, reclassify, duplicate, or otherwise update existing tasks and tickets.
- A project with an existing live Bugs column mapped to backlog must keep it; custom columns must never be replaced.
- Ordinary tasks and tickets must continue entering Backlog or Needs triage, even if Bugs is reordered before that column.
- Explicit destination columns must remain authoritative, including the existing unresolved/resolved bug lifecycle checks.
- Converting a backlog task or ticket into a bug must preserve its identity, ticket membership, evidence, comments, reporter validation, claim guards, version preconditions, and idempotency behavior.
- Converting an active, ready, blocked, or completed task must not silently reset its lifecycle to backlog. Existing completed conversion rejection must remain intact.
- Filing a backlog bug ticket must prefer the destination project's live Bugs/backlog column; filing other ticket states must preserve the existing status mapping.
- An archived, renamed, or reclassified Bugs column must not become a mandatory destination. Fall back to the project's ordinary backlog behavior without creating replacement columns during task mutations.
- A concurrent archive or semantic-state change must not bypass transactional column validation, bug resolution rules, claim guards, or optimistic version checks.
- Resolving a bug must continue using the existing resolution flow; reopening must clear resolution and return to a live backlog column, preferring Bugs.

## Behavior and compatibility

Bugs is an ordinary column with backlog semantics. The existing task kind and bug details remain the source of truth; a column does not restrict which task kinds it accepts. Tickets and bugs continue sharing the same task identity and lifecycle.

Migration 036 adds a live Bugs/backlog column to each existing project lacking a live column with that name and semantic state. It inserts immediately after the first ordinary backlog column, preserving the relative order of existing columns. Later live columns advance their position and resource version once; existing task rows remain untouched. Newly created projects, including the built-in Tickets project, receive Bugs immediately after their ordinary Backlog or Needs triage column.

Bug creation without a destination, backlog task-to-bug conversion without a destination, backlog bug ticket filing, and reopening prefer a live backlog column whose trimmed name equals Bugs, ignoring case. When none exists, normal backlog selection applies. Ordinary task creation prefers a backlog column other than Bugs, falling back to any backlog column when necessary.

Existing API resources, authorization, ticket membership, evidence, numbering aliases, claim controls, optimistic versions, idempotency, release constraints, and bug resolution guards are retained. No migration copies or restores a database, and no existing task is moved automatically.

## Using the connected flow

1. In **Tickets**, open the incoming report and choose **Classify as bug**.
   Add actual behavior and optional expected behavior/reproduction steps, then
   save. This updates the same record rather than creating a second ticket.
2. Choose its project. A backlog bug is filed into that project's **Bugs**
   column. Comments, evidence, and the original ticket-key alias remain intact.
3. Choose severity and priority in **Bug triage**, then select
   **Triage & move to Ready**. The existing bug triage endpoint saves those
   fields and the destination atomically using the task version.
4. Work through the normal board. **Resolve bug…** opens the existing resolution
   workflow; reopening returns the bug to intake. Active or blocked bugs can
   update their triage fields without being reset to Ready.

The board's **Report bug** action defaults to Bugs. Bugs remains an optional,
customizable backlog column; existing bugs are not relocated by the upgrade.

## Repeatable acceptance evidence

Run `test/e2e/run.sh web/e2e/bugs-triage.spec.ts web/e2e/helm-bugs.spec.ts web/e2e/tickets.spec.ts`.
The real browser flow captures desktop/mobile triage and board screenshots plus
synthetic API responses proving the same task identity survives classification,
filing, and acceptance. It also checks ordinary task routing, required severity,
and rejection of stale triage writes. Migration-prefix and retained-binary
regressions verify existing rows and column identities survive migration 036.
