#!/usr/bin/env python3
"""Format pytest-benchmark JSON output as a GitHub-Flavored Markdown table.

Usage:
    python3 format_benchmarks.py <benchmark_json_path>

Writes the table to stdout so the caller can append it to
$GITHUB_STEP_SUMMARY.
"""

import json
import sys


def main() -> None:
    if len(sys.argv) < 2:
        print("_No benchmark JSON path provided._")
        sys.exit(0)

    path = sys.argv[1]
    try:
        with open(path) as fh:
            data = json.load(fh)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"_Could not read benchmark results: {exc}_")
        sys.exit(0)

    benchmarks = data.get("benchmarks", [])
    if not benchmarks:
        print("_No benchmark entries found._")
        sys.exit(0)

    col = 55
    # Header
    print(
        f"| {'Test':<{col}} "
        f"| {'Min ms':>8} "
        f"| {'Mean ms':>8} "
        f"| {'Max ms':>8} "
        f"| {'StdDev':>8} "
        f"| {'Rounds':>6} |"
    )
    # Separator
    print(
        f"|{'-'*(col+2)}"
        f"|{'-'*10}"
        f"|{'-'*10}"
        f"|{'-'*10}"
        f"|{'-'*10}"
        f"|{'-'*8}|"
    )
    for b in sorted(benchmarks, key=lambda x: x["stats"]["mean"]):
        name   = b["name"].replace("test_benchmark_", "")[:col]
        s      = b["stats"]
        stddev = s.get("stddev", 0.0)
        print(
            f"| {name:<{col}} "
            f"| {s['min']*1000:>8.2f} "
            f"| {s['mean']*1000:>8.2f} "
            f"| {s['max']*1000:>8.2f} "
            f"| {stddev*1000:>8.2f} "
            f"| {s['rounds']:>6} |"
        )


if __name__ == "__main__":
    main()
