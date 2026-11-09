#!/usr/bin/env python3
"""Fail-closed event policy; emitted values are data, never shell commands."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

from changes import captured_diff, commit, full_specs, git, read_json, SHA

HERE = Path(__file__).resolve().parent
SCOPES = {'docs', 'fast', 'focused', 'full', 'promotion'}


def promotion_eligible(event, mode):
    return mode == 'enforced' and event.get('name') == 'push' and event.get('ref') == 'refs/heads/main'


def promotion_identity(value, sha):
    """Bounded identity only; the workflow independently resolves its origin."""
    if not isinstance(value, dict):
        raise ValueError('promotion proof must be an object')
    flags = {'accepted': True, 'full_required': False, 'runtime_identity_match': True}
    if any(value.get(key) is not expected for key, expected in flags.items()):
        raise ValueError('promotion proof did not accept exact runtime reuse')
    if value.get('promotion_sha') != sha or value.get('artifact_source_sha') != sha:
        raise ValueError('promotion proof does not bind both source identities to checkout')
    if not isinstance(value.get('image'), str) or not re.fullmatch(
            r'ghcr\.io/kanterlabs/helm@sha256:[0-9a-f]{64}', value['image']):
        raise ValueError('promotion requires an immutable trusted image')
    if not isinstance(value.get('receipt_sha256'), str) or not re.fullmatch(
            r'[0-9a-f]{64}', value['receipt_sha256']):
        raise ValueError('promotion receipt digest is invalid')
    identifiers = ('producer_run_id', 'producer_run_attempt', 'artifact_id')
    if any(type(value.get(key)) is not int or not 0 < value[key] < 2**63 for key in identifiers):
        raise ValueError('promotion producer identity is invalid')
    keys = (*flags, 'promotion_sha', 'artifact_source_sha', 'image', 'receipt_sha256', *identifiers)
    return {key: value[key] for key in keys}


def policy():
    value = read_json(HERE / 'suites.json')
    if value.get('schema_version') != 1 or not value.get('core_smoke') or not value.get('ui_map'):
        raise ValueError('unsupported suite policy')
    digest = hashlib.sha256()
    for name in ('suites.json', 'changes.py', 'plan.py', 'verify-plan.py'):
        digest.update(name.encode() + b'\0' + (HERE / name).read_bytes())
    return value, digest.hexdigest()


def jobs_for(scope):
    if scope == 'promotion':
        return ['checks', 'container']
    if scope == 'full':
        return ['checks', 'browser', 'race', 'container']
    if scope == 'focused':
        return ['checks', 'browser', 'race']
    if scope in ('docs', 'fast'):
        return ['checks']
    raise ValueError('unsupported validation scope')


def classify(diff, rules):
    if not diff['complete']:
        return 'full', ['diff unavailable: ' + str(diff['reason'])], []
    paths = diff['paths']
    if any(item['status'] not in ('A', 'M') for item in paths):
        return 'full', ['rename, deletion or ambiguous change status'], []
    names = [item['path'] for item in paths]

    def docs(name):
        return (name in rules['docs_exact'] or
                (name.startswith('docs/') and name.endswith('.md') and
                 name not in rules['docs_excluded']))

    if all(docs(name) for name in names):
        return 'docs', ['human documentation only'], []
    if all(name in rules['ui_map'] for name in names):
        specs = sorted(set(rules['core_smoke']).union(
            *(rules['ui_map'][name] for name in names)))
        return 'focused', ['all changes match bounded component allowlist'], specs
    return 'full', ['high risk or unclassified path requires full validation'], []


def create(args):
    rules, policy_hash = policy()
    event = read_json(args.event)
    if not isinstance(event, dict):
        raise ValueError('event must be an object')
    sha = git(args.repository, 'rev-parse', 'HEAD').decode().strip()
    commit(args.repository, sha)
    tree = git(args.repository, 'rev-parse', 'HEAD^{tree}').decode().strip()
    if git(args.repository, 'diff', '--name-only', 'HEAD', '--'):
        raise ValueError('tracked checkout differs from recorded source')
    source = {'sha': sha, 'tree': tree, 'head_sha': None, 'base_sha': None,
              'merge_base_sha': None, 'merge_sha': None}
    metadata = {'name': args.event_name, 'action': event.get('action'), 'ref': event.get('ref'),
                'base_ref': None, 'draft': False}
    diff = {'complete': False, 'paths': [], 'reason': 'manual validation', 'merge_base_sha': None}
    if args.event_name == 'pull_request':
        pr = event.get('pull_request', {})
        base, head = pr.get('base', {}), pr.get('head', {})
        if base.get('ref') not in ('beta', 'main') or type(pr.get('draft')) is not bool:
            raise ValueError('unsupported PR target or draft status')
        metadata.update(base_ref=base['ref'], draft=pr['draft'])
        source.update(head_sha=head.get('sha'), base_sha=base.get('sha'))
        # Raw object parents bind even a depth-one checkout to the captured PR.
        # History may be unavailable for its diff; that selects full below.
        captured_head = head.get('sha')
        if not isinstance(captured_head, str) or not SHA.fullmatch(captured_head):
            raise ValueError('PR requires a captured head commit')
        if sha != captured_head:
            header = git(args.repository, 'cat-file', '-p', sha).split(b'\n\n', 1)[0]
            parents = [line[7:].decode('ascii') for line in header.splitlines()
                       if line.startswith(b'parent ')]
            if len(parents) != 2 or set(parents) != {captured_head, base.get('sha')}:
                raise ValueError('checkout is not the captured PR merge')
            source['merge_sha'] = sha
        diff = captured_diff(args.repository, base.get('sha'), head.get('sha'), True,
                             pr.get('changed_files'))
        if diff['complete'] and source['merge_sha'] is None and diff['merge_base_sha'] != base.get('sha'):
            diff.update(complete=False, reason='head checkout does not include advancing captured base')
        proposed, reasons, focused = classify(diff, rules)
        if pr['draft']:
            proposed, reasons, focused = 'fast', ['draft feedback; not ready-to-merge validation'], []
        elif base['ref'] == 'main':
            proposed, reasons, focused = 'full', ['ready PR targeting main requires full validation'], []
    elif args.event_name == 'push':
        if event.get('ref') not in ('refs/heads/beta', 'refs/heads/main') or event.get('deleted'):
            raise ValueError('unsupported push target')
        if commit(args.repository, event.get('after')) != sha:
            raise ValueError('checkout does not match push after revision')
        source.update(head_sha=event['after'], base_sha=event.get('before'))
        diff = captured_diff(args.repository, event.get('before'), event['after'])
        proposed, reasons, focused = classify(diff, rules)
        if proposed != 'docs':
            proposed, reasons, focused = 'full', ['trusted runtime push requires full validation'] + reasons, []
    elif args.event_name == 'workflow_dispatch':
        proposed = event.get('inputs', {}).get('scope', 'full')
        if proposed not in ('fast', 'focused', 'full'):
            raise ValueError('manual scope must be fast, focused or full')
        reasons, focused = ['explicit validation only; no publication or deployment'], rules['core_smoke']
        source['head_sha'] = sha
    else:
        raise ValueError('unsupported CI event')
    promotion, promotion_fallback = None, False
    if args.promotion and promotion_eligible(metadata, args.mode):
        try:
            promotion = promotion_identity(read_json(args.promotion), sha)
            proposed, reasons, focused = 'promotion', ['reuse independently verified full beta validation'], []
        except (ValueError, OSError, KeyError, TypeError):
            proposed, reasons, focused = 'full', ['promotion proof unavailable or invalid; full fallback'], []
            promotion_fallback = True
    source['merge_base_sha'] = diff['merge_base_sha']
    effective = 'full' if args.mode == 'shadow' else proposed
    available = full_specs(args.repository, sha)
    selected = sorted(available + ['github']) if effective == 'full' else (
        sorted(focused) if effective == 'focused' else [])
    if effective in ('focused', 'full') and (
            not available or any(spec != 'github' and spec not in available for spec in selected)):
        raise ValueError('selected suite does not exist at checkout')
    release = args.event_name == 'push' and proposed != 'docs'
    value = {'schema_version': 1, 'mode': args.mode, 'event': metadata, 'source': source,
            'diff': diff, 'policy_hash': policy_hash, 'proposed_scope': proposed,
            'effective_scope': effective, 'reasons': reasons, 'selected_suites': selected,
            'expected_jobs': jobs_for(effective), 'publish': release, 'deploy': release,
            'merge_ready': args.event_name == 'pull_request' and not metadata['draft']}
    if promotion is not None:
        value['promotion'] = promotion
    if promotion_fallback:
        value['promotion_fallback'] = True
    return value


def validate_plan(value):
    if value.get('schema_version') != 1 or value.get('mode') not in ('shadow', 'enforced'):
        raise ValueError('unsupported plan schema or mode')
    scope = value.get('effective_scope')
    if value.get('proposed_scope') not in SCOPES or value.get('expected_jobs') != jobs_for(scope):
        raise ValueError('plan scope/job inventory mismatch')
    if value['mode'] == 'shadow' and scope != 'full':
        raise ValueError('shadow plan did not preserve full contexts')
    if value['mode'] == 'enforced' and scope != value['proposed_scope']:
        raise ValueError('effective scope differs from enforced proposal')
    for key in ('sha', 'tree'):
        if not isinstance(value['source'].get(key), str) or not SHA.fullmatch(value['source'][key]):
            raise ValueError('invalid source identity')
    rules, policy_hash = policy()
    if value.get('policy_hash') != policy_hash:
        raise ValueError('plan policy does not match current verifier')
    event = value.get('event', {})
    name = event.get('name')
    diff = value.get('diff', {})
    if type(diff.get('complete')) is not bool or not isinstance(diff.get('paths'), list):
        raise ValueError('plan has no captured diff state')
    if name in ('pull_request', 'push'):
        proposed, _reasons, focused = classify(diff, rules)
        if name == 'pull_request':
            if event.get('base_ref') not in ('beta', 'main') or type(event.get('draft')) is not bool:
                raise ValueError('unsupported PR plan metadata')
            if event['draft']:
                proposed, focused = 'fast', []
            elif event['base_ref'] == 'main':
                proposed, focused = 'full', []
        else:
            if event.get('ref') not in ('refs/heads/beta', 'refs/heads/main'):
                raise ValueError('unsupported push plan metadata')
            if proposed != 'docs':
                proposed, focused = 'full', []
    elif name == 'workflow_dispatch':
        proposed, focused = value['proposed_scope'], rules['core_smoke']
        if proposed not in ('fast', 'focused', 'full'):
            raise ValueError('unsupported manual plan scope')
    else:
        raise ValueError('unsupported plan event')
    if 'promotion' in value or 'promotion_fallback' in value:
        if not promotion_eligible(event, value['mode']):
            raise ValueError('event is ineligible for promotion proof')
        if 'promotion' in value:
            identity = promotion_identity(value['promotion'], value['source']['sha'])
            if identity != value['promotion'] or 'promotion_fallback' in value:
                raise ValueError('promotion plan contains unvalidated fields or contradictory fallback')
            proposed, focused = 'promotion', []
        elif value['promotion_fallback'] is True:
            proposed, focused = 'full', []
        else:
            raise ValueError('invalid promotion fallback marker')
    if proposed != value['proposed_scope']:
        raise ValueError('proposed scope does not match captured event and changes')
    release = name == 'push' and proposed != 'docs'
    if value.get('publish') is not release or value.get('deploy') is not release:
        raise ValueError('publication/deployment flags violate event policy')
    merge_ready = name == 'pull_request' and not event['draft']
    if value.get('merge_ready') is not merge_ready:
        raise ValueError('plan misrepresents merge readiness')
    suites = value.get('selected_suites')
    if not isinstance(suites, list) or suites != sorted(set(suites)):
        raise ValueError('invalid suite inventory')
    if scope in ('docs', 'fast', 'promotion') and suites:
        raise ValueError('narrow plan has unexpected browser suites')
    if scope in ('focused', 'full') and not suites:
        raise ValueError('browser plan has no suites')
    if scope == 'full' and 'github' not in suites:
        raise ValueError('full plan is missing GitHub integration')
    if scope == 'focused' and suites != sorted(set(focused)):
        raise ValueError('focused inventory does not match captured allowlisted changes')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--event', type=Path, required=True)
    parser.add_argument('--event-name', required=True)
    parser.add_argument('--repository', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--github-output', type=Path)
    parser.add_argument('--promotion', type=Path)
    parser.add_argument('--mode', choices=['shadow', 'enforced'], default='shadow')
    args = parser.parse_args()
    try:
        value = create(args)
        validate_plan(value)
        args.output.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')
        if args.github_output:
            outputs = {key: str(key in value['expected_jobs']).lower()
                       for key in ('checks', 'browser', 'race', 'container')}
            outputs.update({key: str(value[key]).lower() for key in ('publish', 'deploy')})
            outputs.update({key: value[key] for key in ('proposed_scope', 'effective_scope')})
            outputs['suites_json'] = json.dumps(value['selected_suites'], separators=(',', ':'))
            with args.github_output.open('a') as stream:
                stream.write(''.join(f'{key}={item}\n' for key, item in outputs.items()))
        return 0
    except (ValueError, OSError, KeyError, TypeError) as error:
        print(f'CI plan rejected: {error}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
