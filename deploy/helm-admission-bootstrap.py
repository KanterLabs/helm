#!/usr/bin/env python3
"""Root operator only: preview or prepare permanent CLOSED admission authority."""
import argparse
import json
import sys

from admission.bootstrap import BootstrapError, prepare, preview


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    inspect = commands.add_parser('preview', help='read source/backup; print a bounded plan to save privately')
    inspect.add_argument('--lane', choices=('beta', 'production'), required=True)
    inspect.add_argument('--backup', required=True, help='existing exact snapshot basename under the lane backups directory')
    inspect.add_argument('--image', required=True)
    inspect.add_argument('--release-sha', required=True)
    inspect.add_argument('--binary-sha256', required=True)
    inspect.add_argument('--nonce')
    change = commands.add_parser('prepare', help='recheck a root-private saved plan and prepare CLOSED authority')
    change.add_argument('--plan', required=True)
    args = parser.parse_args()
    try:
        if args.command == 'preview':
            value = preview(args.lane, args.backup, args.image, args.release_sha, args.binary_sha256, args.nonce)
        else:
            value = prepare(args.plan)
        print(json.dumps(value, sort_keys=True, separators=(',', ':')))
        return 0
    except BootstrapError as error:
        print('CLOSED bootstrap refused: ' + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
