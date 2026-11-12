"""Result checks shared by the Gitea shadow soak proofs.

Each helper returns a check over one tool run: the parsed ledger, the summary,
stderr and the exit status. A failed check raises AssertionError with the
observed value, so a proof that fails says why.
"""


def commit(ledger, sha):
    found = [c for c in ledger["commits"] if c["sha"] == sha or c["sha"].startswith(sha)]
    if len(found) != 1:
        raise AssertionError(f"expected one ledger commit for {sha}, found {len(found)}")
    return found[0]


def expect(condition, message):
    if not condition:
        raise AssertionError(message)


def is_class(sha, klass, reason=None):
    def check(result):
        entry = commit(result["ledger"], sha)
        expect(entry["class"] == klass, f"{sha[:8]} is {entry['class']} {entry['reasons']}, want {klass}")
        if reason:
            expect(reason in entry["reasons"], f"{sha[:8]} reasons {entry['reasons']} lack {reason}")
    return check


def streak(count, ended_by=None):
    def check(result):
        got = result["ledger"]["streak"]
        expect(got["count"] == count, f"streak {got['count']}, want {count}")
        end = (got["ended_by"] or {}).get("sha")
        expect(end == ended_by, f"streak ended by {end}, want {ended_by}")
    return check


def both(*checks):
    def check(result):
        for item in checks:
            item(result)
    return check


def exits(status, phrase=None, ledger_written=None):
    def check(result):
        expect(result["exit"] == status, f"exit {result['exit']}, want {status}: {result['stderr'][-300:]}")
        if phrase:
            expect(phrase in result["stderr"], f"stderr lacks {phrase!r}: {result['stderr'][-300:]}")
        if ledger_written is not None:
            expect((result["ledger"] is not None) == ledger_written, "ledger presence is wrong")
    return check


def gate_unmet(part):
    def check(result):
        gate = result["ledger"]["gate"]
        expect(gate["met"] is False and gate[part] is False, f"gate {gate}")
    return check
