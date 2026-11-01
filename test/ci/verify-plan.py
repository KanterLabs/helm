#!/usr/bin/env python3
"""Verify job results against a declared scope and real browser artifacts."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

from changes import read_json
from plan import policy, promotion_identity, validate_plan

HEX = re.compile(r'[0-9a-f]{64}\Z')


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def report_spec(name):
    if not isinstance(name, str):
        return None
    name = name.replace('\\', '/')
    if '/web/e2e/' in name:
        name = 'e2e/' + name.split('/web/e2e/', 1)[1]
    elif name.startswith('web/e2e/'):
        name = name[4:]
    elif not name.startswith('e2e/'):
        name = 'e2e/' + name
    if '..' in name.split('/') or not name.endswith('.spec.ts'):
        return None
    return name


def report_inventory(report):
    if report.get('errors') or report.get('stats', {}).get('unexpected', 0):
        raise ValueError('browser report contains failures or errors')
    inventory = set()
    observed = set()

    def walk(suite, inherited=None):
        name = report_spec(suite.get('file')) or inherited
        for spec in suite.get('specs', []):
            filename = report_spec(spec.get('file')) or name
            if not filename:
                raise ValueError('report test has no source spec')
            tests = spec.get('tests', [])
            if tests:
                observed.add(filename)
            for test in tests:
                if test.get('status') not in ('expected', 'flaky', 'skipped'):
                    raise ValueError('report includes unexpected or unexecuted test')
                attempts = test.get('results', [])
                if any(attempt.get('status') == 'passed' for attempt in attempts):
                    inventory.add(filename)
        for child in suite.get('suites', []):
            walk(child, name)

    for suite in report.get('suites', []):
        walk(suite)
    return inventory, observed


def browser_evidence(directory, plan):
    if directory is None or not directory.is_dir():
        raise ValueError('selected browser job requires its execution artifact')
    hashes = {}

    def take(relative):
        path = directory / relative
        if path.is_symlink() or not path.is_file():
            raise ValueError('browser evidence file missing or unsafe: ' + relative)
        hashes[relative] = digest(path)
        return path

    metadata = {}
    for line in take('evidence.metadata').read_text().splitlines():
        key, separator, value = line.partition('=')
        if not separator or key in metadata:
            raise ValueError('invalid browser metadata')
        metadata[key] = value
    if metadata.get('revision') != plan['source']['sha'] or metadata.get('exit_status') != '0':
        raise ValueError('browser metadata source or exit status mismatch')
    provenance = read_json(take('build-provenance.json'))
    source = provenance.get('source', {})
    if (source.get('head') != plan['source']['sha'] or
            not HEX.fullmatch(str(source.get('fingerprint', ''))) or
            provenance.get('configuration', {}).get('race') is not True):
        raise ValueError('browser build provenance source/race mismatch')
    expected_binaries = ['helm', 'helm-githubbeta'] if plan['effective_scope'] == 'full' else ['helm']
    for binary in expected_binaries:
        if not HEX.fullmatch(str(provenance.get('binaries', {}).get(binary, {}).get('sha256', ''))):
            raise ValueError('browser build provenance missing binary identity')
    summary = read_json(take('suites/suites.json'))
    if summary.get('signal', 0) or summary.get('error'):
        raise ValueError('browser suite coordinator failed or was interrupted')
    phases = {}
    for phase in summary['phases']:
        name = phase['name']
        if name in phases:
            raise ValueError('duplicate suite coordinator phase')
        phases[name] = phase
        if phase.get('required') is True and (phase.get('status') != 'passed' or
                                             phase.get('exit_status') != 0):
            raise ValueError('selected browser phase did not pass')
    rules = policy()[0]
    required = list(rules['full_phases']) if plan['effective_scope'] == 'full' else ['main']
    if plan['effective_scope'] == 'full':
        required += [name for name, spec in rules.get('conditional_phases', {}).items()
                     if spec in plan['selected_suites']]
    for name in required:
        phase = phases.get(name, {})
        if phase.get('required') is not True or phase.get('status') != 'passed' or phase.get('exit_status') != 0:
            raise ValueError('required browser phase missing or incomplete: ' + name)
    reports = sorted(directory.rglob('results.json'))
    if not reports:
        raise ValueError('browser artifact has no Playwright reports')
    passed, observed = set(), set()
    for report in reports:
        relative = report.relative_to(directory).as_posix()
        actual, discovered = report_inventory(read_json(take(relative)))
        passed.update(actual)
        observed.update(discovered)
    selected = set(plan['selected_suites']) - {'github'}
    missing = selected - passed
    if missing:
        raise ValueError('selected specs lack passed execution: ' + ', '.join(sorted(missing)))
    # Full reports enumerate every spec; a plan that dropped a selected file
    # cannot silently relabel the broader executed inventory as the full plan.
    if plan['effective_scope'] == 'full' and observed != selected:
        raise ValueError('full plan and discovered report inventory differ')
    executed = sorted(selected | ({'github'} if 'github' in plan['selected_suites'] else set()))
    return {'digests': hashes, 'actual_inventory': sorted(passed | ({'github'} if
            'github' in plan['selected_suites'] else set())),
            'observed_inventory': sorted(observed), 'phases': sorted(phases),
            'source_fingerprint': source['fingerprint'],
            'evidence_sha256': hashlib.sha256(json.dumps(hashes, sort_keys=True).encode()).hexdigest()}, executed


def verify(args):
    receipt = {'schema_version': 1, 'format': 1, 'passed': False, 'accepted': False,
               'errors': [], 'jobs': {}, 'results': {}, 'selected_suites': [],
               'executed_suites': [], 'browser': {}}
    try:
        plan = read_json(args.plan)
        validate_plan(plan)
        results = read_json(args.results)
        if not isinstance(results, dict):
            raise ValueError('job results must be an object')
        receipt.update(effective_scope=plan['effective_scope'], scope=plan['effective_scope'],
                       proposed_scope=plan['proposed_scope'], source=plan['source'],
                       source_sha=plan['source']['sha'], source_tree=plan['source']['tree'],
                       policy_hash=plan['policy_hash'], plan_sha256=digest(args.plan),
                       selected_suites=plan['selected_suites'])
        for job in plan['expected_jobs']:
            result = results.get(job, {}).get('result')
            receipt['jobs'][job] = result
            if result != 'success':
                receipt['errors'].append(f'selected job {job} did not succeed: {result}')
        receipt['results'] = dict(receipt['jobs'])
        if plan['effective_scope'] == 'promotion':
            if args.promotion_proof is None:
                raise ValueError('promotion scope requires a freshly resolved proof')
            proof = promotion_identity(read_json(args.promotion_proof), plan['source']['sha'])
            if proof != plan['promotion']:
                raise ValueError('fresh promotion proof differs from planned identity')
            canonical = json.dumps(proof, sort_keys=True, separators=(',', ':')).encode()
            receipt['reused_full_validation'] = {**proof, 'proof_sha256': hashlib.sha256(canonical).hexdigest()}
        if 'browser' in plan['expected_jobs']:
            browser, executed = browser_evidence(args.browser_evidence, plan)
            receipt.update(browser=browser, executed_suites=executed,
                           evidence_sha256=browser['evidence_sha256'])
        receipt['passed'] = receipt['accepted'] = not receipt['errors']
    except (ValueError, OSError, KeyError, TypeError, AttributeError) as error:
        receipt['errors'].append(str(error))
    args.output.write_text(json.dumps(receipt, indent=2, sort_keys=True) + '\n')
    if not receipt['passed']:
        print('CI gate rejected: ' + '; '.join(receipt['errors']), file=sys.stderr)
    return 0 if receipt['passed'] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', type=Path, required=True)
    parser.add_argument('--results', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--browser-evidence', type=Path)
    parser.add_argument('--promotion-proof', type=Path)
    return verify(parser.parse_args())


if __name__ == '__main__':
    sys.exit(main())
