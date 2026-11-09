"""Threshold boundary and page chain on a fixed evaluation clock: plan case 14.

Case 14 owns a server running the test-clock binary (built with
`-tags helm_testclock`, see API.md), whose evaluation instant is fixed to
T0 through HELM_TEST_EVALUATION_TIME. Default builds cannot fix the clock.
Moved from cases_signal.py by TC-380; the page-chain and sort checks are
unchanged, the exact-instant checks are new.
"""
from contract import Checks, activity, ids, listing, neglected
from harness import Server, World, parse


def case_14_boundary_and_page_chain(ctx, receipt):
    clock = ctx.clock
    server = Server(ctx.clock_binary, ctx.workdir, name='clock',
                    extra_env={'HELM_TEST_EVALUATION_TIME': clock.ago(0)})
    server.start()
    try:
        _boundary(server, clock, receipt)
    finally:
        server.stop()


def _boundary(server, clock, receipt):
    w = World(server, clock, key='CLK')
    cards = [w.old_task(f'Synthetic page chain card {n}', 30 + n) for n in range(3)]
    at_threshold = w.old_task('Synthetic card exactly at the threshold', 14)
    newer = w.task('Synthetic card one millisecond newer')
    w.backdate(newer, clock.ago(14, seconds=-0.001))
    w.touch(newer)
    receipt['fixture'] = {'cards': ['T0-30d', 'T0-31d', 'T0-32d'], 'page_limit': 1,
                          'evaluation_clock': 'T0 (test-clock binary)',
                          'boundary_cards': {'at_threshold': 'T0-14d', 'newer': 'T0-14d+1ms'}}
    checks = Checks(receipt)

    def one_evaluated_at():
        tasks, pages = neglected(server, w.key, 14, limit=1)
        assert len(pages) >= 3, pages
        assert len({page['evaluated_at'] for page in pages}) == 1, pages
        assert parse(pages[0]['evaluated_at']) == clock.t0, pages[0]
        assert {c['id'] for c in cards} <= set(ids(tasks)), 'page chain skipped a card'
        assert len(ids(tasks)) == len(set(ids(tasks))), 'page chain repeated a card'
        return pages

    def exact_threshold():
        tasks, pages = neglected(server, w.key, 14, limit=200)
        assert at_threshold['id'] in ids(tasks), 'card at evaluated_at - 14d is not inactive'
        assert newer['id'] not in ids(tasks), 'card 1 ms newer than the threshold is inactive'
        value = activity(w.get(newer))
        assert parse(value['not_after']) == parse(clock.ago(14, seconds=-0.001)), value
        return {'evaluated_at': pages[0]['evaluated_at'], 'inactive': len(tasks)}

    def sorted_by_not_after():
        _, body = listing(server, w.key, inactive_days=14, sort='not_after', limit=200)
        bounds = [activity(t)['not_after'] for t in body['data']]
        assert None not in bounds and bounds == sorted(bounds, key=parse), bounds
        return len(bounds)

    checks.check('one_evaluated_at_governs_page_chain', one_evaluated_at)
    checks.check('threshold_instant_is_exact', exact_threshold)
    checks.check('sort_not_after_orders_oldest_first', sorted_by_not_after)
    checks.finish()


CASES = {14: case_14_boundary_and_page_chain}
