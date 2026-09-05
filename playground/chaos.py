#!/usr/bin/env python3
"""Break the playground on purpose so every f9s detector has something to
yell about. Submits pathological SQL jobs through the SQL Gateway — no
custom jars needed.

Jobs:
  chaos-backpressure — datagen at 200k rows/s into a deliberately
      CPU-saturated MD5 chain: the expensive operator pins busy% and
      backpressures the source (sentinel + early-warning + advisor).
  chaos-skew — every row keyed to ONE group at parallelism 2: one hot
      subtask, one idle (skew alarm + drill-down + cause classifier).
"""
import json
import sys
import time
import urllib.request

GW = "http://localhost:8083"


def post(path, body):
    req = urllib.request.Request(GW + path, data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req, timeout=30))


def cancel_previous_chaos():
    """Make reruns idempotent: cancel leftover chaos jobs first."""
    try:
        jobs = json.load(urllib.request.urlopen("http://localhost:8081/jobs/overview", timeout=10))["jobs"]
    except Exception:
        return
    for j in jobs:
        if "chaos_" in j.get("name", "") and j["state"] in ("RUNNING", "CREATED", "RESTARTING"):
            req = urllib.request.Request(
                f"http://localhost:8081/jobs/{j['jid']}?mode=cancel", method="PATCH")
            try:
                urllib.request.urlopen(req, timeout=10)
            except Exception:
                pass


def run(session, statement):
    op = post(f"/v2/sessions/{session}/statements", {"statement": statement})
    return op["operationHandle"]


def main():
    cancel_previous_chaos()
    session = post("/v2/sessions", {"properties": {
        "execution.runtime-mode": "streaming",
        "parallelism.default": "2",
        # chaining off forces a network shuffle between the datagen source
        # and the expensive Calc — without the exchange there is no
        # backpressure signal to detect.
        "pipeline.operator-chaining": "false",
    }})["sessionHandle"]

    ddl = [
        "CREATE TABLE chaos_src (a BIGINT) WITH ('connector'='datagen','rows-per-second'='2000000')",
        "CREATE TABLE chaos_sink (b STRING) WITH ('connector'='blackhole')",
        "CREATE TABLE chaos_agg_sink (k BIGINT, c BIGINT) WITH ('connector'='blackhole')",
    ]
    for stmt in ddl:
        run(session, stmt)
        time.sleep(1)

    md5 = "CAST(a AS STRING)"
    for _ in range(64):
        md5 = f"MD5({md5})"
    run(session, f"INSERT INTO chaos_sink SELECT {md5} FROM chaos_src /* chaos-backpressure */")
    time.sleep(2)
    run(session, "INSERT INTO chaos_agg_sink SELECT MOD(a,1), COUNT(*) FROM chaos_src GROUP BY MOD(a,1) /* chaos-skew */")

    print("chaos submitted: backpressure + skew jobs are ramping.")
    print("open ./f9s — expect the ⚠ badge within ~60s; press ! for alerts,")
    print("enter a red job for the bottleneck banner, d for the skew story.")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 — a demo script should explain itself
        print(f"chaos setup failed: {e}", file=sys.stderr)
        print("is the playground up? run: make playground", file=sys.stderr)
        sys.exit(1)
