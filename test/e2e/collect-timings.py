#!/usr/bin/env python3
"""Reduce Playwright JSON reports to safe file-duration metadata.

Supply the full commit SHA that produced the reports, verified from the run's
provenance. This collector cannot infer source identity from a Playwright report.
"""
import argparse
import json
from pathlib import Path
import sys

from browser_timings import collect_reports


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-revision", required=True)
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("reports", nargs="+", type=Path)
    args = parser.parse_args()
    try:
        profile = collect_reports(args.reports, args.repo, args.source_revision)
    except (ValueError, KeyError, TypeError, OSError) as error:
        print(f"Cannot collect file timings: {type(error).__name__}", file=sys.stderr)
        return 1
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(profile, indent=2) + "\n")
    print(f"Collected {len(profile['files'])} measured files from {len(args.reports)} reports")
    return 0


if __name__ == "__main__":
    sys.exit(main())
