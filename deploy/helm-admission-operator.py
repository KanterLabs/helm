#!/usr/bin/env python3
"""Native root operator only: close/rebind or activate accepted prepared authority."""
import argparse
import json
import sys

from admission.filesystem import BootstrapError
from admission.operator import transition


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', required=True, help='strict root-private reviewed transition plan')
    args = parser.parse_args()
    try:
        print(json.dumps(transition(args.plan), sort_keys=True, separators=(',', ':')))
        return 0
    except BootstrapError as error:
        print('Native admission operator refused: ' + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
