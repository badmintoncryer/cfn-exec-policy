#!/usr/bin/env python3
"""Turn CloudTrail into candidate rows for no-handler types (issue #5).

Reads windows.log ("<type-file> create|update|delete|end <UTC time>" lines written by
the deploy loop), fetches the CloudTrail events CloudFormation made on behalf of the
caller in each window, and prints the IAM actions per type and phase.

Usage: bench/nohandler/harvest.py [windows.log] [--user Administrator]
"""
import collections, datetime, json, subprocess, sys

# CloudTrail eventSource host -> IAM service prefix, where they differ.
PREFIX = {"monitoring": "cloudwatch", "email": "ses", "elasticloadbalancing": "elasticloadbalancing"}


def parse(t):
    return datetime.datetime.fromisoformat(t.replace("Z", "+00:00"))


def events(start, end, user):
    out = subprocess.run(
        ["aws", "cloudtrail", "lookup-events", "--no-cli-pager", "--region", "us-east-1",
         "--start-time", start.isoformat(), "--end-time", end.isoformat(),
         "--lookup-attributes", f"AttributeKey=Username,AttributeValue={user}", "--output", "json"],
        check=True, capture_output=True, text=True).stdout
    for e in json.loads(out)["Events"]:
        yield json.loads(e["CloudTrailEvent"])


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    user = sys.argv[sys.argv.index("--user") + 1] if "--user" in sys.argv else "Administrator"
    if "--user" in sys.argv:
        args.remove(user)
    marks = collections.defaultdict(dict)
    for line in open(args[0] if args else "windows.log"):
        t, phase, at = line.split()
        marks[t][phase] = parse(at)
    start = min(m["create"] for m in marks.values())
    end = max(m.get("end", m["create"]) for m in marks.values()) + datetime.timedelta(minutes=1)
    evs = [e for e in events(start, end, user) if e.get("userIdentity", {}).get("invokedBy") == "cloudformation.amazonaws.com"]
    rows = {}
    for t, m in marks.items():
        bounds = [("create", m["create"], m["update"]), ("update", m["update"], m["delete"]), ("delete", m["delete"], m["end"])]
        rows[t] = {}
        for phase, a, b in bounds:
            acts = set()
            for e in evs:
                if a <= parse(e["eventTime"]) < b:
                    svc = e["eventSource"].split(".")[0]
                    acts.add(f"{PREFIX.get(svc, svc)}:{e['eventName']}")
            rows[t][phase] = sorted(acts)
        rows[t]["all"] = sorted(set().union(*rows[t].values()))
    json.dump(rows, sys.stdout, indent=2)
    print()


if __name__ == "__main__":
    main()
