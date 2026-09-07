#!/usr/bin/env python3
"""Read bouncer's audit log.

Two questions this answers that `bouncer log` does not:

  recent   which prompts happened, and can each one be replayed faithfully?
  counts   how many prompts a month does each rule actually cost?

The counts matter because bouncer's default list was chosen by measurement, not
by taste. Changing a rule without knowing what it costs is how the list rots.

Every command found by `recent` is also written to a file, so replaying it is
`bouncer explain --cwd DIR - < FILE` with no shell quoting to get wrong.
"""

from __future__ import annotations

import argparse
import collections
import datetime as dt
import json
import os
import pathlib
import re
import sys

LOG_DIR = pathlib.Path.home() / ".claude" / "logs"

# Lines are capped at 1 KB by the logger, which truncates the command and marks
# the cut with an ellipsis. A line near the cap ending that way is truncated;
# checking the length as well keeps `go build ./...` from looking cut.
LINE_CAP = 1024
NEAR_CAP = 950


def parse_since(text: str) -> dt.timedelta:
    """Accept 30d, 24h or 45m."""
    match = re.fullmatch(r"(\d+)([dhm])", text.strip())
    if not match:
        raise argparse.ArgumentTypeError(f"expected something like 7d, 24h or 30m, got {text!r}")

    amount, unit = int(match.group(1)), match.group(2)
    return {"d": dt.timedelta(days=amount), "h": dt.timedelta(hours=amount), "m": dt.timedelta(minutes=amount)}[unit]


def month_files(since: dt.datetime) -> list[pathlib.Path]:
    """List the monthly log files that can hold records at or after since."""
    files, month = [], since.replace(day=1)
    end = dt.datetime.now(dt.timezone.utc)

    while month <= end:
        path = LOG_DIR / f"bouncer-{month:%Y-%m}.jsonl"
        if path.exists():
            files.append(path)
        month = (month.replace(day=28) + dt.timedelta(days=7)).replace(day=1)

    return files


def read(since: dt.datetime) -> list[dict]:
    """Read every record at or after since, oldest first."""
    records = []

    for path in month_files(since):
        for raw in path.read_text(errors="replace").splitlines():
            raw = raw.strip()
            if not raw.startswith("{"):
                continue

            try:
                record = json.loads(raw)
            except json.JSONDecodeError:
                # a torn line must not hide the rest of the month.
                continue

            when = record.get("ts", "")
            try:
                record["_when"] = dt.datetime.fromisoformat(when.replace("Z", "+00:00"))
            except ValueError:
                continue

            if record["_when"] < since:
                continue

            body = record.get("input", "")
            record["_truncated"] = len(raw) > NEAR_CAP and body.endswith("...")
            records.append(record)

    return sorted(records, key=lambda r: r["_when"])


def scratch_dir() -> pathlib.Path:
    """Somewhere to park full command text for replaying."""
    base = pathlib.Path(os.environ.get("TMPDIR", "/tmp")) / "bouncer-asks"
    base.mkdir(parents=True, exist_ok=True)
    return base


def cmd_recent(args: argparse.Namespace) -> int:
    since = dt.datetime.now(dt.timezone.utc) - parse_since(args.since)
    records = [r for r in read(since) if args.outcome == "any" or r.get("outcome") == args.outcome]

    if not records:
        print(f"no {args.outcome} records in the last {args.since}.")
        print("if you expected some, check bouncer is registered: bouncer rules")
        return 0

    where = scratch_dir()

    for index, record in enumerate(reversed(records[-args.limit :]), start=1):
        body = record.get("input", "")
        target = where / f"ask-{index}.txt"
        target.write_text(body)

        local = record["_when"].astimezone().strftime("%Y-%m-%d %H:%M")
        print(f"[{index}] {local}  {record.get('outcome', '?')}  rule={record.get('rule') or '(none)'}  tool={record.get('tool', '?')}")
        print(f"    cwd: {record.get('cwd') or '(none recorded)'}")

        if record.get("error"):
            print(f"    error: {record['error']}")
            print("    bouncer could not understand this, so it prompted. The rule field is not the cause.")

        print(f"    command: {' '.join(body.split())[:160]}")

        if record["_truncated"]:
            print("    TRUNCATED at the 1 KB log cap. Replaying this gives a WRONG answer,")
            print("    because a cut-off heredoc or quote fails to parse. Ask the user for the")
            print("    full command, or find it in the session transcript, before diagnosing.")
        else:
            cwd = record.get("cwd") or "."
            print(f"    replay: bouncer explain --cwd {cwd} --tool {record.get('tool', 'Bash')} - < {target}")

        print()

    return 0


def cmd_counts(args: argparse.Namespace) -> int:
    since = dt.datetime.now(dt.timezone.utc) - parse_since(args.since)
    records = read(since)

    if not records:
        print(f"no records in the last {args.since}.")
        return 0

    asks = [r for r in records if r.get("outcome") == "ask"]
    by_rule = collections.Counter(r.get("rule") or "(no rule: bouncer errored)" for r in asks)

    span_days = max((records[-1]["_when"] - records[0]["_when"]).total_seconds() / 86400, 1)

    print(f"{len(records)} decisions over {span_days:.1f} days: {len(records) - len(asks)} silent, {len(asks)} prompted.")
    print(f"that is {len(asks) / span_days * 30:.0f} prompts a month at this rate.\n")
    print(f"{'prompts':>7}  {'per month':>9}  rule")

    for rule, count in by_rule.most_common():
        print(f"{count:>7}  {count / span_days * 30:>9.0f}  {rule}")

    print("\nA rule near the top is worth a look: either it is catching something real")
    print("often, or it is misfiring. Read the actual commands with `recent` before deciding.")

    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="mode", required=True)

    recent = sub.add_parser("recent", help="recent prompts, newest first, with a replay command for each")
    recent.add_argument("--since", default="7d", help="window, e.g. 7d, 24h, 30m (default 7d)")
    recent.add_argument("--limit", type=int, default=5, help="how many to show (default 5)")
    recent.add_argument("--outcome", default="ask", choices=["ask", "allow", "any"], help="default ask")
    recent.set_defaults(func=cmd_recent)

    counts = sub.add_parser("counts", help="how many prompts each rule costs")
    counts.add_argument("--since", default="30d", help="window, e.g. 30d (default 30d)")
    counts.set_defaults(func=cmd_counts)

    args = parser.parse_args()

    if not LOG_DIR.exists():
        print(f"no log directory at {LOG_DIR}.", file=sys.stderr)
        print("bouncer may not be enabled yet. Check with: bouncer rules", file=sys.stderr)
        return 1

    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
